package project

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

var workspaceRootIgnoreDenylist = []string{
	"node_modules/",
	"dist/",
	"build/",
	".cache/",
	".turbo/",
	"target/",
	"coverage/",
	"tmp/",
	"temp/",
}

func prepareWorkspaceProject(ctx context.Context, parent string, projectID domain.ProjectID, registeredAt time.Time) ([]domain.WorkspaceRepoRecord, error) {
	children, err := detectWorkspaceChildren(ctx, parent, projectID, registeredAt)
	if err != nil {
		return nil, err
	}
	if len(children) == 0 {
		return nil, apierr.Invalid("WORKSPACE_REPOS_REQUIRED", "Workspace project must contain at least one direct child git repository", map[string]any{
			"suggestedFix": "Create or move child repositories directly under the workspace folder, then retry.",
		})
	}
	if isGitRepo(parent) {
		if err := adoptWorkspaceParent(ctx, parent, children); err != nil {
			return nil, err
		}
	} else {
		if err := initWorkspaceParent(ctx, parent, children); err != nil {
			return nil, err
		}
	}
	if err := guardNoGitlinks(ctx, parent); err != nil {
		return nil, err
	}
	return children, nil
}

func detectWorkspaceChildren(ctx context.Context, parent string, projectID domain.ProjectID, registeredAt time.Time) ([]domain.WorkspaceRepoRecord, error) {
	entries, err := os.ReadDir(parent)
	if err != nil {
		return nil, apierr.Invalid("INVALID_PATH", "Workspace path could not be read", nil)
	}
	var repos []domain.WorkspaceRepoRecord
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == ".git" {
			continue
		}
		child := filepath.Join(parent, name)
		if !isGitRepo(child) {
			continue
		}
		if err := validateWorkspaceChild(ctx, child); err != nil {
			return nil, err
		}
		repos = append(repos, domain.WorkspaceRepoRecord{
			ProjectID:     projectID,
			Name:          name,
			RelativePath:  filepath.ToSlash(name),
			RepoOriginURL: resolveGitOriginURL(child),
			RegisteredAt:  registeredAt,
		})
	}
	sort.Slice(repos, func(i, j int) bool { return repos[i].Name < repos[j].Name })
	return repos, nil
}

func validateWorkspaceChild(ctx context.Context, child string) error {
	gitPath := filepath.Join(child, ".git")
	info, err := os.Lstat(gitPath)
	if err != nil {
		return apierr.Invalid("INVALID_WORKSPACE_CHILD", "Workspace child repository is missing a .git directory", map[string]any{"path": child})
	}
	if !info.IsDir() {
		return apierr.Invalid("WORKSPACE_CHILD_IS_WORKTREE", "Workspace child repositories must be standalone repos, not worktrees of an external repo", map[string]any{
			"path":         child,
			"suggestedFix": "Register a standalone child repository, or clone/init it directly under the workspace parent.",
		})
	}
	if out, err := gitOutput(ctx, child, "rev-parse", "--is-bare-repository"); err != nil {
		return apierr.Invalid("INVALID_WORKSPACE_CHILD", "Workspace child repository could not be inspected", map[string]any{"path": child, "error": err.Error()})
	} else if strings.TrimSpace(out) == "true" {
		return apierr.Invalid("WORKSPACE_CHILD_BARE", "Workspace child repositories must not be bare repositories", map[string]any{"path": child})
	}
	if _, err := gitOutput(ctx, child, "rev-parse", "--verify", "HEAD"); err != nil {
		return apierr.Invalid("WORKSPACE_CHILD_UNBORN", "Workspace child repositories must have at least one commit", map[string]any{
			"path":         child,
			"suggestedFix": "Run `git init -b main`, add the initial files, and create the first commit before registering the workspace.",
		})
	}
	branch, err := gitOutput(ctx, child, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil || strings.TrimSpace(branch) == "" {
		return apierr.Invalid("WORKSPACE_CHILD_DEFAULT_BRANCH_UNKNOWN", "Workspace child repositories must have an identifiable default branch", map[string]any{
			"path":         child,
			"suggestedFix": "Check out the repository's default branch (for example `main`) and retry.",
		})
	}
	return nil
}

func adoptWorkspaceParent(ctx context.Context, parent string, repos []domain.WorkspaceRepoRecord) error {
	changed, err := ensureWorkspaceGitignore(parent, repos)
	if err != nil {
		return apierr.Invalid("WORKSPACE_PARENT_GITIGNORE_FAILED", "Failed to update workspace parent .gitignore", map[string]any{"error": err.Error()})
	}
	if !changed {
		return nil
	}
	if _, err := gitOutput(ctx, parent, "add", ".gitignore"); err != nil {
		return apierr.Invalid("WORKSPACE_PARENT_GITIGNORE_FAILED", "Failed to stage workspace parent .gitignore", map[string]any{"error": err.Error()})
	}
	if err := guardNoGitlinks(ctx, parent); err != nil {
		return err
	}
	if _, err := gitOutput(ctx, parent, "commit", "-m", "chore: configure AO workspace ignores", "--", ".gitignore"); err != nil {
		return apierr.Invalid("WORKSPACE_PARENT_COMMIT_FAILED", "Failed to commit workspace parent .gitignore", map[string]any{"error": err.Error()})
	}
	return nil
}

func initWorkspaceParent(ctx context.Context, parent string, repos []domain.WorkspaceRepoRecord) error {
	if _, err := gitOutput(ctx, parent, "init", "-b", domain.DefaultBranchName); err != nil {
		return apierr.Invalid("WORKSPACE_PARENT_INIT_FAILED", "Failed to initialize workspace parent git repository", map[string]any{"error": err.Error()})
	}
	if _, err := ensureWorkspaceGitignore(parent, repos); err != nil {
		return apierr.Invalid("WORKSPACE_PARENT_GITIGNORE_FAILED", "Failed to write workspace parent .gitignore", map[string]any{"error": err.Error()})
	}
	if _, err := gitOutput(ctx, parent, "add", "-A"); err != nil {
		return apierr.Invalid("WORKSPACE_PARENT_ADD_FAILED", "Failed to stage workspace parent files", map[string]any{"error": err.Error()})
	}
	if err := guardNoGitlinks(ctx, parent); err != nil {
		return err
	}
	if _, err := gitOutput(ctx, parent, "commit", "-m", "chore: initialize AO workspace root"); err != nil {
		return apierr.Invalid("WORKSPACE_PARENT_COMMIT_FAILED", "Failed to create workspace parent initial commit", map[string]any{"error": err.Error()})
	}
	return nil
}

func ensureWorkspaceGitignore(parent string, repos []domain.WorkspaceRepoRecord) (bool, error) {
	path := filepath.Join(parent, ".gitignore")
	seen := map[string]bool{}
	var lines []string
	if data, err := os.ReadFile(path); err == nil {
		s := bufio.NewScanner(strings.NewReader(string(data)))
		for s.Scan() {
			line := s.Text()
			lines = append(lines, line)
			seen[strings.TrimSpace(line)] = true
		}
		if err := s.Err(); err != nil {
			return false, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}

	var additions []string
	for _, repo := range repos {
		additions = append(additions, "/"+filepath.ToSlash(repo.RelativePath)+"/")
	}
	additions = append(additions, workspaceRootIgnoreDenylist...)
	changed := false
	for _, entry := range additions {
		if seen[entry] {
			continue
		}
		lines = append(lines, entry)
		seen[entry] = true
		changed = true
	}
	if !changed {
		return false, nil
	}
	content := strings.Join(lines, "\n")
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return true, os.WriteFile(path, []byte(content), 0o644)
}

func guardNoGitlinks(ctx context.Context, repo string) error {
	out, err := gitOutput(ctx, repo, "ls-files", "-s")
	if err != nil {
		return apierr.Invalid("WORKSPACE_PARENT_INDEX_FAILED", "Failed to inspect workspace parent index", map[string]any{"error": err.Error()})
	}
	var paths []string
	s := bufio.NewScanner(strings.NewReader(out))
	for s.Scan() {
		line := s.Text()
		if strings.HasPrefix(line, "160000 ") {
			_, path, _ := strings.Cut(line, "\t")
			paths = append(paths, path)
		}
	}
	if err := s.Err(); err != nil {
		return apierr.Invalid("WORKSPACE_PARENT_INDEX_FAILED", "Failed to inspect workspace parent index", map[string]any{"error": err.Error()})
	}
	if len(paths) > 0 {
		return apierr.Invalid("WORKSPACE_PARENT_GITLINK", "Workspace parent index contains embedded gitlinks; child repos must be gitignored before committing", map[string]any{"paths": paths})
	}
	return nil
}

func workspaceReposFromRecords(records []domain.WorkspaceRepoRecord) []WorkspaceRepo {
	out := make([]WorkspaceRepo, 0, len(records))
	for _, rec := range records {
		out = append(out, WorkspaceRepo{Name: rec.Name, RelativePath: rec.RelativePath, Repo: rec.RepoOriginURL})
	}
	return out
}

func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git -C %s %s: %w: %s", dir, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
