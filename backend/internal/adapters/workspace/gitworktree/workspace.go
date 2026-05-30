package gitworktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	defaultGitBinary = "git"
	defaultBranch    = "main"
)

var (
	ErrUnsafePath = errors.New("gitworktree: unsafe workspace path")
)

type RepoResolver interface {
	RepoPath(projectID domain.ProjectID) (string, error)
}

type StaticRepoResolver map[domain.ProjectID]string

func (r StaticRepoResolver) RepoPath(projectID domain.ProjectID) (string, error) {
	path := r[projectID]
	if path == "" {
		return "", fmt.Errorf("gitworktree: no repo configured for project %q", projectID)
	}
	return path, nil
}

type Options struct {
	Binary        string
	ManagedRoot   string
	DefaultBranch string
	RepoResolver  RepoResolver
}

type Workspace struct {
	binary        string
	managedRoot   string
	defaultBranch string
	repos         RepoResolver
	run           commandRunner
}

type commandRunner func(ctx context.Context, binary string, args ...string) ([]byte, error)

var _ ports.Workspace = (*Workspace)(nil)

func New(opts Options) (*Workspace, error) {
	binary := opts.Binary
	if binary == "" {
		binary = defaultGitBinary
	}
	branch := opts.DefaultBranch
	if branch == "" {
		branch = defaultBranch
	}
	if opts.ManagedRoot == "" {
		return nil, errors.New("gitworktree: ManagedRoot is required")
	}
	if opts.RepoResolver == nil {
		return nil, errors.New("gitworktree: RepoResolver is required")
	}
	root, err := physicalAbs(opts.ManagedRoot)
	if err != nil {
		return nil, fmt.Errorf("gitworktree: managed root: %w", err)
	}
	return &Workspace{
		binary:        binary,
		managedRoot:   filepath.Clean(root),
		defaultBranch: branch,
		repos:         opts.RepoResolver,
		run:           runCommand,
	}, nil
}

func (w *Workspace) Create(ctx context.Context, cfg ports.WorkspaceConfig) (ports.WorkspaceInfo, error) {
	if err := validateConfig(cfg); err != nil {
		return ports.WorkspaceInfo{}, err
	}
	repo, err := w.repoPath(cfg.ProjectID)
	if err != nil {
		return ports.WorkspaceInfo{}, err
	}
	if err := w.validateBranch(ctx, repo, cfg.Branch); err != nil {
		return ports.WorkspaceInfo{}, err
	}
	path, err := w.managedPath(cfg.ProjectID, cfg.SessionID)
	if err != nil {
		return ports.WorkspaceInfo{}, err
	}
	if err := w.addWorktree(ctx, repo, path, cfg.Branch); err != nil {
		return ports.WorkspaceInfo{}, err
	}
	return ports.WorkspaceInfo{Path: path, Branch: cfg.Branch, SessionID: cfg.SessionID, ProjectID: cfg.ProjectID}, nil
}

func (w *Workspace) Destroy(ctx context.Context, info ports.WorkspaceInfo) error {
	if info.ProjectID == "" {
		return errors.New("gitworktree: project id is required")
	}
	if info.Path == "" {
		return fmt.Errorf("%w: empty path", ErrUnsafePath)
	}
	repo, err := w.repoPath(info.ProjectID)
	if err != nil {
		return err
	}
	path, err := w.validateManagedPath(info.Path)
	if err != nil {
		return err
	}
	_, removeErr := w.run(ctx, w.binary, worktreeRemoveArgs(repo, path)...)
	if _, err := w.run(ctx, w.binary, worktreePruneArgs(repo)...); err != nil {
		return fmt.Errorf("gitworktree: worktree prune: %w", err)
	}
	records, err := w.listRecords(ctx, repo)
	if err != nil {
		return err
	}
	if worktreeRegistered(records, path) {
		if removeErr != nil {
			return fmt.Errorf("gitworktree: refusing to remove %q: path is still registered after git worktree prune (worktree remove: %w)", path, removeErr)
		}
		return fmt.Errorf("gitworktree: refusing to remove %q: path is still registered after git worktree prune", path)
	}
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("gitworktree: remove unregistered path %q: %w", path, err)
	}
	return nil
}

func (w *Workspace) List(ctx context.Context, project domain.ProjectID) ([]ports.WorkspaceInfo, error) {
	if project == "" {
		return nil, errors.New("gitworktree: project id is required")
	}
	repo, err := w.repoPath(project)
	if err != nil {
		return nil, err
	}
	records, err := w.listRecords(ctx, repo)
	if err != nil {
		return nil, err
	}
	projectRoot, err := w.projectRoot(project)
	if err != nil {
		return nil, err
	}
	return filterProjectWorktrees(records, projectRoot, project), nil
}

func (w *Workspace) Restore(ctx context.Context, cfg ports.WorkspaceConfig) (ports.WorkspaceInfo, error) {
	if err := validateConfig(cfg); err != nil {
		return ports.WorkspaceInfo{}, err
	}
	repo, err := w.repoPath(cfg.ProjectID)
	if err != nil {
		return ports.WorkspaceInfo{}, err
	}
	path, err := w.managedPath(cfg.ProjectID, cfg.SessionID)
	if err != nil {
		return ports.WorkspaceInfo{}, err
	}
	records, err := w.listRecords(ctx, repo)
	if err != nil {
		return ports.WorkspaceInfo{}, err
	}
	if rec, ok := findWorktree(records, path); ok {
		branch := rec.Branch
		if branch == "" {
			branch = cfg.Branch
		}
		return ports.WorkspaceInfo{Path: path, Branch: branch, SessionID: cfg.SessionID, ProjectID: cfg.ProjectID}, nil
	}
	if nonEmpty, err := pathExistsNonEmpty(path); err != nil {
		return ports.WorkspaceInfo{}, err
	} else if nonEmpty {
		return ports.WorkspaceInfo{}, fmt.Errorf("gitworktree: refusing to restore %q: path exists and is not a registered worktree", path)
	}
	if err := w.validateBranch(ctx, repo, cfg.Branch); err != nil {
		return ports.WorkspaceInfo{}, err
	}
	if err := w.addWorktree(ctx, repo, path, cfg.Branch); err != nil {
		return ports.WorkspaceInfo{}, err
	}
	return ports.WorkspaceInfo{Path: path, Branch: cfg.Branch, SessionID: cfg.SessionID, ProjectID: cfg.ProjectID}, nil
}

func (w *Workspace) addWorktree(ctx context.Context, repo, path, branch string) error {
	localBranch, err := w.refExists(ctx, repo, "refs/heads/"+branch)
	if err != nil {
		return err
	}
	if localBranch {
		if _, err := w.run(ctx, w.binary, chooseWorktreeAddArgs(repo, path, branch, "", true)...); err != nil {
			return fmt.Errorf("gitworktree: worktree add existing branch %q: %w", branch, err)
		}
		return nil
	}
	baseRef, err := w.resolveBaseRef(ctx, repo, branch)
	if err != nil {
		return err
	}
	if _, err := w.run(ctx, w.binary, chooseWorktreeAddArgs(repo, path, branch, baseRef, false)...); err != nil {
		return fmt.Errorf("gitworktree: worktree add branch %q from %q: %w", branch, baseRef, err)
	}
	return nil
}

func (w *Workspace) validateBranch(ctx context.Context, repo, branch string) error {
	if _, err := w.run(ctx, w.binary, checkRefFormatBranchArgs(repo, branch)...); err != nil {
		return fmt.Errorf("gitworktree: invalid branch %q: %w", branch, err)
	}
	return nil
}

func (w *Workspace) resolveBaseRef(ctx context.Context, repo, branch string) (string, error) {
	candidates := baseRefCandidates(branch, w.defaultBranch)
	for _, ref := range candidates {
		exists, err := w.refExists(ctx, repo, ref)
		if err != nil {
			return "", err
		}
		if exists {
			return ref, nil
		}
	}
	return "", fmt.Errorf("gitworktree: no base ref found for branch %q (tried %s)", branch, strings.Join(candidates, ", "))
}

func (w *Workspace) refExists(ctx context.Context, repo, ref string) (bool, error) {
	_, err := w.run(ctx, w.binary, revParseVerifyArgs(repo, ref)...)
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("gitworktree: verify ref %q: %w", ref, err)
}

func (w *Workspace) listRecords(ctx context.Context, repo string) ([]worktreeRecord, error) {
	out, err := w.run(ctx, w.binary, worktreeListPorcelainArgs(repo)...)
	if err != nil {
		return nil, fmt.Errorf("gitworktree: worktree list: %w", err)
	}
	records, err := parseWorktreePorcelain(string(out))
	if err != nil {
		return nil, fmt.Errorf("gitworktree: parse worktree list: %w", err)
	}
	return records, nil
}

func (w *Workspace) repoPath(project domain.ProjectID) (string, error) {
	repo, err := w.repos.RepoPath(project)
	if err != nil {
		return "", err
	}
	if repo == "" {
		return "", fmt.Errorf("gitworktree: no repo configured for project %q", project)
	}
	abs, err := physicalAbs(repo)
	if err != nil {
		return "", fmt.Errorf("gitworktree: repo path: %w", err)
	}
	return abs, nil
}

func physicalAbs(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return filepath.Clean(resolved), nil
	}
	parent := filepath.Dir(abs)
	base := filepath.Base(abs)
	for parent != "." && parent != string(os.PathSeparator) {
		if resolved, err := filepath.EvalSymlinks(parent); err == nil {
			return filepath.Join(resolved, base), nil
		}
		base = filepath.Join(filepath.Base(parent), base)
		parent = filepath.Dir(parent)
	}
	if resolved, err := filepath.EvalSymlinks(parent); err == nil {
		return filepath.Join(resolved, base), nil
	}
	return abs, nil
}

func validateConfig(cfg ports.WorkspaceConfig) error {
	if cfg.ProjectID == "" {
		return errors.New("gitworktree: project id is required")
	}
	if err := validatePathComponent("project id", string(cfg.ProjectID)); err != nil {
		return err
	}
	if cfg.SessionID == "" {
		return errors.New("gitworktree: session id is required")
	}
	if err := validatePathComponent("session id", string(cfg.SessionID)); err != nil {
		return err
	}
	if cfg.Branch == "" {
		return errors.New("gitworktree: branch is required")
	}
	return nil
}

// validatePathComponent rejects id values that could escape the managed root
// once joined into a path. filepath.Join cleans `..` before validateManagedPath
// runs, so a session id of "../other" would otherwise resolve back inside
// managedRoot while breaking per-project isolation. Reject any path separator
// or the special `.`/`..` components at the source.
func validatePathComponent(name, value string) error {
	if strings.ContainsAny(value, `/\`) {
		return fmt.Errorf("%w: %s %q must not contain path separators", ErrUnsafePath, name, value)
	}
	if value == "." || value == ".." {
		return fmt.Errorf("%w: %s %q must not be a path-traversal component", ErrUnsafePath, name, value)
	}
	return nil
}

func (w *Workspace) managedPath(project domain.ProjectID, session domain.SessionID) (string, error) {
	path := filepath.Join(w.managedRoot, string(project), string(session))
	return w.validateManagedPath(path)
}

func (w *Workspace) projectRoot(project domain.ProjectID) (string, error) {
	path := filepath.Join(w.managedRoot, string(project))
	return w.validateManagedPath(path)
}

func (w *Workspace) validateManagedPath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("%w: empty path", ErrUnsafePath)
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("%w: %q is not absolute", ErrUnsafePath, path)
	}
	clean := filepath.Clean(path)
	if clean != path {
		return "", fmt.Errorf("%w: %q is not clean", ErrUnsafePath, path)
	}
	physical, err := physicalAbs(clean)
	if err != nil {
		return "", fmt.Errorf("gitworktree: resolve path %q: %w", path, err)
	}
	clean = physical
	inside, err := pathWithin(w.managedRoot, clean)
	if err != nil {
		return "", err
	}
	if !inside || clean == w.managedRoot {
		return "", fmt.Errorf("%w: %q is outside managed root %q", ErrUnsafePath, clean, w.managedRoot)
	}
	return clean, nil
}

func pathWithin(root, path string) (bool, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false, fmt.Errorf("gitworktree: compare paths: %w", err)
	}
	return rel == "." || (rel != "" && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))), nil
}

func filterProjectWorktrees(records []worktreeRecord, projectRoot string, project domain.ProjectID) []ports.WorkspaceInfo {
	out := make([]ports.WorkspaceInfo, 0, len(records))
	for _, rec := range records {
		path := filepath.Clean(rec.Path)
		inside, err := pathWithin(projectRoot, path)
		if err != nil || !inside || path == projectRoot {
			continue
		}
		out = append(out, ports.WorkspaceInfo{
			Path:      path,
			Branch:    rec.Branch,
			SessionID: domain.SessionID(filepath.Base(path)),
			ProjectID: project,
		})
	}
	return out
}

func worktreeRegistered(records []worktreeRecord, path string) bool {
	_, ok := findWorktree(records, path)
	return ok
}

func findWorktree(records []worktreeRecord, path string) (worktreeRecord, bool) {
	clean := filepath.Clean(path)
	for _, rec := range records {
		if filepath.Clean(rec.Path) == clean {
			return rec, true
		}
	}
	return worktreeRecord{}, false
}

func pathExistsNonEmpty(path string) (bool, error) {
	entries, err := os.ReadDir(path)
	if err == nil {
		return len(entries) > 0, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("gitworktree: inspect path %q: %w", path, err)
}

func runCommand(ctx context.Context, binary string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, commandError{args: append([]string{binary}, args...), output: string(out), err: err}
	}
	return out, nil
}

type commandError struct {
	args   []string
	output string
	err    error
}

func (e commandError) Error() string {
	if strings.TrimSpace(e.output) == "" {
		return fmt.Sprintf("%s: %v", strings.Join(e.args, " "), e.err)
	}
	return fmt.Sprintf("%s: %v: %s", strings.Join(e.args, " "), e.err, strings.TrimSpace(e.output))
}

func (e commandError) Unwrap() error { return e.err }
