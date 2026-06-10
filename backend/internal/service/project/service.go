package project

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// Manager is the controller-facing contract for the /api/v1/projects surface.
type Manager interface {
	// List returns every registered project, including degraded entries
	// (those whose config failed to load but whose registry entry survives).
	List(ctx context.Context) ([]Summary, error)

	// Get returns one project, discriminating ok vs degraded via GetResult.
	Get(ctx context.Context, id domain.ProjectID) (GetResult, error)

	// Add registers a new project from a git repository path.
	Add(ctx context.Context, in AddInput) (Project, error)

	// SetConfig replaces a project's per-project config, returning the updated
	// read-model.
	SetConfig(ctx context.Context, id domain.ProjectID, in SetConfigInput) (Project, error)

	// Remove unregisters a project, stopping its sessions and reclaiming
	// managed workspaces.
	Remove(ctx context.Context, id domain.ProjectID) (RemoveResult, error)
}

// Service implements project registration and lookup use-cases for controllers.
type Service struct {
	store Store
	// addMu serialises the whole body of Add. Workspace registration performs
	// filesystem mutations (git init, .gitignore writes, commits) that are not
	// covered by the store's own writeMu, so path/id conflict checks plus the
	// subsequent mutation must be atomic from the perspective of concurrent callers.
	addMu sync.Mutex
}

var _ Manager = (*Service)(nil)

// New returns a project service backed by the given durable store.
func New(store Store) *Service {
	return &Service{store: store}
}

// List returns every active registered project.
func (m *Service) List(ctx context.Context) ([]Summary, error) {
	projects, err := m.store.ListProjects(ctx)
	if err != nil {
		return nil, apierr.Internal("PROJECTS_LIST_FAILED", "Failed to load projects")
	}
	out := make([]Summary, 0, len(projects))
	for _, row := range projects {
		out = append(out, Summary{
			ID:            domain.ProjectID(row.ID),
			Name:          displayName(row),
			Path:          row.Path,
			Kind:          row.Kind.WithDefault(),
			SessionPrefix: resolveSessionPrefix(row),
		})
	}
	return out, nil
}

// Get returns one active project by id.
func (m *Service) Get(ctx context.Context, id domain.ProjectID) (GetResult, error) {
	if err := validateProjectID(id); err != nil {
		return GetResult{}, err
	}
	row, ok, err := m.store.GetProject(ctx, string(id))
	if err != nil {
		return GetResult{}, apierr.Internal("PROJECT_LOAD_FAILED", "Failed to load project")
	}
	if !ok || !row.ArchivedAt.IsZero() {
		return GetResult{}, apierr.NotFound("PROJECT_NOT_FOUND", "Unknown project")
	}
	p := projectFromRow(row)
	if row.Kind.WithDefault() == domain.ProjectKindWorkspace {
		repos, err := m.store.ListWorkspaceRepos(ctx, row.ID)
		if err != nil {
			return GetResult{}, apierr.Internal("PROJECT_LOAD_FAILED", "Failed to load workspace repositories")
		}
		p.WorkspaceRepos = workspaceReposFromRecords(repos)
	}
	return GetResult{Status: "ok", Project: &p}, nil
}

// Add registers a local git repository as a project.
//
// The whole method body is serialised by addMu because workspace registration
// mutates the filesystem (git init, .gitignore, commits) between the conflict
// check and the store write — two concurrent calls for the same path would both
// pass FindProjectByPath and then race on those mutations.
func (m *Service) Add(ctx context.Context, in AddInput) (Project, error) {
	path, err := normalizePath(in.Path)
	if err != nil {
		return Project{}, err
	}
	id := defaultProjectID(path)
	if in.ProjectID != nil {
		id = domain.ProjectID(strings.TrimSpace(*in.ProjectID))
	}
	if err := validateProjectID(id); err != nil {
		return Project{}, err
	}

	m.addMu.Lock()
	defer m.addMu.Unlock()

	name := string(id)
	if in.Name != nil {
		name = strings.TrimSpace(*in.Name)
	}
	if name == "" {
		name = string(id)
	}

	if existing, ok, err := m.store.FindProjectByPath(ctx, path); err != nil {
		return Project{}, apierr.Internal("PROJECT_LOAD_FAILED", "Failed to load project")
	} else if ok {
		return Project{}, apierr.Conflict("PATH_ALREADY_REGISTERED", "A project at this path is already registered", map[string]any{
			"existingProjectId":  existing.ID,
			"suggestedProjectId": string(m.suggestID(ctx, id)),
		})
	}
	if existing, ok, err := m.store.GetProject(ctx, string(id)); err != nil {
		return Project{}, apierr.Internal("PROJECT_LOAD_FAILED", "Failed to load project")
	} else if ok && existing.ArchivedAt.IsZero() && existing.Path != path {
		return Project{}, apierr.Conflict("ID_ALREADY_REGISTERED", "A project with this id is already registered for a different path", map[string]any{
			"existingProjectId":  existing.ID,
			"suggestedProjectId": string(m.suggestID(ctx, id)),
		})
	}

	var config domain.ProjectConfig
	if in.Config != nil {
		if err := in.Config.Validate(); err != nil {
			return Project{}, apierr.Invalid("INVALID_PROJECT_CONFIG", err.Error(), nil)
		}
		config = *in.Config
	}

	registeredAt := time.Now()
	row := domain.ProjectRecord{
		ID:           string(id),
		Path:         path,
		DisplayName:  name,
		RegisteredAt: registeredAt,
		Kind:         domain.ProjectKindSingleRepo,
		Config:       config,
	}
	if in.AsWorkspace {
		repos, err := prepareWorkspaceProject(ctx, path, domain.ProjectID(row.ID), registeredAt)
		if err != nil {
			return Project{}, err
		}
		row.Kind = domain.ProjectKindWorkspace
		row.RepoOriginURL = resolveGitOriginURL(path)
		if err := m.store.UpsertWorkspaceProject(ctx, row, repos); err != nil {
			return Project{}, apierr.Internal("PROJECT_ADD_FAILED", "Failed to register workspace project")
		}
		p := projectFromRow(row)
		p.WorkspaceRepos = workspaceReposFromRecords(repos)
		return p, nil
	}
	if !isGitRepo(path) {
		return Project{}, apierr.Invalid("NOT_A_GIT_REPO", "Repository path must point to a git repository", nil)
	}
	row.RepoOriginURL = resolveGitOriginURL(path)
	if err := m.store.UpsertProject(ctx, row); err != nil {
		return Project{}, apierr.Internal("PROJECT_ADD_FAILED", "Failed to register project")
	}
	return projectFromRow(row), nil
}

// SetConfig replaces the project's stored config. The typed config is validated
// here so a bad value is rejected when set rather than surfacing at spawn.
func (m *Service) SetConfig(ctx context.Context, id domain.ProjectID, in SetConfigInput) (Project, error) {
	if err := validateProjectID(id); err != nil {
		return Project{}, err
	}
	if err := in.Config.Validate(); err != nil {
		return Project{}, apierr.Invalid("INVALID_PROJECT_CONFIG", err.Error(), nil)
	}
	row, ok, err := m.store.GetProject(ctx, string(id))
	if err != nil {
		return Project{}, apierr.Internal("PROJECT_LOAD_FAILED", "Failed to load project")
	}
	if !ok || !row.ArchivedAt.IsZero() {
		return Project{}, apierr.NotFound("PROJECT_NOT_FOUND", "Unknown project")
	}
	row.Config = in.Config
	if err := m.store.UpsertProject(ctx, row); err != nil {
		return Project{}, apierr.Internal("PROJECT_CONFIG_UPDATE_FAILED", "Failed to update project config")
	}
	return projectFromRow(row), nil
}

// resolveGitOriginURL returns the project's `origin` remote URL via
// `git -C path remote get-url origin`. A missing remote, missing repo, or any
// other git error returns an empty string — `project add` must not fail just
// because no origin is configured (the SCM observer skips such projects).
func resolveGitOriginURL(path string) string {
	out, err := exec.Command("git", "-C", path, "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// Remove archives a project registration.
func (m *Service) Remove(ctx context.Context, id domain.ProjectID) (RemoveResult, error) {
	if err := validateProjectID(id); err != nil {
		return RemoveResult{}, err
	}
	ok, err := m.store.ArchiveProject(ctx, string(id), time.Now())
	if err != nil {
		return RemoveResult{}, apierr.Internal("PROJECT_REMOVE_FAILED", "Failed to remove project")
	}
	if !ok {
		return RemoveResult{}, apierr.NotFound("PROJECT_NOT_FOUND", "Unknown project")
	}
	return RemoveResult{ProjectID: id, RemovedStorageDir: false}, nil
}

func (m *Service) suggestID(ctx context.Context, base domain.ProjectID) domain.ProjectID {
	for i := 1; ; i++ {
		candidate := domain.ProjectID(string(base) + strconv.Itoa(i))
		if _, ok, _ := m.store.GetProject(ctx, string(candidate)); !ok {
			return candidate
		}
	}
}

func projectFromRow(row domain.ProjectRecord) Project {
	p := Project{
		ID:            domain.ProjectID(row.ID),
		Name:          displayName(row),
		Kind:          row.Kind.WithDefault(),
		Path:          row.Path,
		Repo:          row.RepoOriginURL,
		DefaultBranch: row.Config.WithDefaults().DefaultBranch,
	}
	if !row.Config.IsZero() {
		cfg := row.Config
		p.Config = &cfg
	}
	return p
}

func displayName(row domain.ProjectRecord) string {
	if strings.TrimSpace(row.DisplayName) != "" {
		return row.DisplayName
	}
	return row.ID
}

func normalizePath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", apierr.Invalid("PATH_REQUIRED", "Repository path is required", nil)
	}
	if strings.HasPrefix(raw, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", apierr.Invalid("INVALID_PATH", "Repository path could not be expanded", nil)
		}
		if raw == "~" {
			raw = home
		} else if strings.HasPrefix(raw, "~/") || strings.HasPrefix(raw, `~\`) {
			raw = filepath.Join(home, raw[2:])
		}
	}
	abs, err := filepath.Abs(raw)
	if err != nil {
		return "", apierr.Invalid("INVALID_PATH", "Repository path is invalid", nil)
	}
	return filepath.Clean(abs), nil
}

func isGitRepo(path string) bool {
	cmd := exec.Command("git", "-C", path, "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	top := filepath.Clean(strings.TrimSpace(string(out)))
	path = filepath.Clean(path)
	top, err = filepath.EvalSymlinks(top)
	if err != nil {
		return false
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}

	if strings.EqualFold(top, path) {
		return true
	}
	return top == path
}

func defaultProjectID(path string) domain.ProjectID {
	id := strings.ToLower(filepath.Base(path))
	id = strings.TrimSpace(id)
	id = strings.ReplaceAll(id, " ", "-")
	return domain.ProjectID(id)
}

var projectIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func validateProjectID(id domain.ProjectID) error {
	raw := string(id)
	// Reject any "." run: a "." prefix fails the pattern, but an embedded ".."
	// (e.g. "a..b") passes it yet yields a branch like "ao/a..b-1" that git's
	// check-ref-format rejects — surfacing as an opaque 500 at spawn time.
	if raw == "" || raw == "." || strings.Contains(raw, "..") || strings.ContainsAny(raw, `/\`) || !projectIDPattern.MatchString(raw) {
		return apierr.Invalid("INVALID_PROJECT_ID", "Project id failed storage-path validation", nil)
	}
	return nil
}

// resolveSessionPrefix prefers an explicit per-project SessionPrefix and falls
// back to the id-derived prefix. (Display only; session-id generation is
// unchanged.)
func resolveSessionPrefix(row domain.ProjectRecord) string {
	if p := strings.TrimSpace(row.Config.SessionPrefix); p != "" {
		return p
	}
	return sessionPrefix(row.ID)
}

func sessionPrefix(id string) string {
	if id == "" {
		return "ao"
	}
	if len(id) <= 12 {
		return id
	}
	return id[:12]
}
