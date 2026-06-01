package project

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/httpx"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// manager is the registry-backed project.Manager. It calls the existing sqlite
// project store directly (no extra store/port/adapter); the on-disk behaviour
// config is not yet wired, so Get always resolves "ok" and UpdateConfig reports
// that config persistence is unavailable. Errors are httpx.APIErr values the
// controller translates to the locked envelope.
type manager struct {
	store *sqlite.Store
}

var _ Manager = (*manager)(nil)

// NewManager builds the project Manager over the durable sqlite store.
func NewManager(store *sqlite.Store) Manager {
	return &manager{store: store}
}

func (m *manager) List(ctx context.Context) ([]Summary, error) {
	rows, err := m.store.ListProjects(ctx)
	if err != nil {
		return nil, httpx.Internal("PROJECTS_LIST_FAILED", "Failed to load projects")
	}
	out := make([]Summary, 0, len(rows))
	for _, row := range rows {
		out = append(out, Summary{
			ID:            domain.ProjectID(row.ID),
			Name:          displayName(row),
			SessionPrefix: sessionPrefix(row.ID),
		})
	}
	return out, nil
}

func (m *manager) Get(ctx context.Context, id domain.ProjectID) (GetResult, error) {
	if err := validateProjectID(id); err != nil {
		return GetResult{}, err
	}
	row, ok, err := m.store.GetProject(ctx, string(id))
	if err != nil {
		return GetResult{}, httpx.Internal("PROJECT_LOAD_FAILED", "Failed to load project")
	}
	if !ok {
		return GetResult{}, httpx.NotFound("PROJECT_NOT_FOUND", "Unknown project")
	}
	p := projectFromRow(row)
	return GetResult{Status: "ok", Project: &p}, nil
}

func (m *manager) Add(ctx context.Context, in AddInput) (Project, error) {
	path, err := normalizePath(in.Path)
	if err != nil {
		return Project{}, err
	}
	if !isGitRepo(path) {
		return Project{}, httpx.BadRequest("NOT_A_GIT_REPO", "Repository path must point to a git repository", nil)
	}

	id := defaultProjectID(path)
	if in.ProjectID != nil {
		id = domain.ProjectID(strings.TrimSpace(*in.ProjectID))
	}
	if err := validateProjectID(id); err != nil {
		return Project{}, err
	}

	name := string(id)
	if in.Name != nil && strings.TrimSpace(*in.Name) != "" {
		name = strings.TrimSpace(*in.Name)
	}

	if existing, ok, err := m.findByPath(ctx, path); err != nil {
		return Project{}, httpx.Internal("PROJECT_LOAD_FAILED", "Failed to load project")
	} else if ok {
		return Project{}, httpx.Conflict("PATH_ALREADY_REGISTERED", "A project at this path is already registered", map[string]any{
			"existingProjectId":  existing.ID,
			"suggestedProjectId": string(m.suggestID(ctx, id)),
		})
	}
	if existing, ok, err := m.store.GetProject(ctx, string(id)); err != nil {
		return Project{}, httpx.Internal("PROJECT_LOAD_FAILED", "Failed to load project")
	} else if ok && existing.Path != path {
		return Project{}, httpx.Conflict("ID_ALREADY_REGISTERED", "A project with this id is already registered for a different path", map[string]any{
			"existingProjectId":  existing.ID,
			"suggestedProjectId": string(m.suggestID(ctx, id)),
		})
	}

	row := sqlite.ProjectRow{
		ID:           string(id),
		Path:         path,
		DisplayName:  name,
		RegisteredAt: time.Now(),
	}
	if err := m.store.UpsertProject(ctx, row); err != nil {
		return Project{}, httpx.Internal("PROJECT_ADD_FAILED", "Failed to add project")
	}
	return projectFromRow(row), nil
}

func (m *manager) UpdateConfig(ctx context.Context, id domain.ProjectID, _ UpdateConfigInput) (Project, error) {
	if err := validateProjectID(id); err != nil {
		return Project{}, err
	}
	if _, ok, err := m.store.GetProject(ctx, string(id)); err != nil {
		return Project{}, httpx.Internal("PROJECT_LOAD_FAILED", "Failed to load project")
	} else if !ok {
		return Project{}, httpx.NotFound("PROJECT_NOT_FOUND", "Unknown project")
	}
	// Identity is frozen and behaviour-config persistence isn't wired yet, so
	// there is nothing this patch can durably change.
	return Project{}, httpx.Conflict("PROJECT_CONFIG_UNAVAILABLE",
		"Project config patching is unavailable until config persistence is wired", nil)
}

func (m *manager) Remove(ctx context.Context, id domain.ProjectID) (RemoveResult, error) {
	if err := validateProjectID(id); err != nil {
		return RemoveResult{}, err
	}
	if _, ok, err := m.store.GetProject(ctx, string(id)); err != nil {
		return RemoveResult{}, httpx.Internal("PROJECT_REMOVE_FAILED", "Failed to remove project")
	} else if !ok {
		return RemoveResult{}, httpx.NotFound("PROJECT_NOT_FOUND", "Unknown project")
	}
	if err := m.store.ArchiveProject(ctx, string(id), time.Now()); err != nil {
		return RemoveResult{}, httpx.Internal("PROJECT_REMOVE_FAILED", "Failed to remove project")
	}
	// removedStorageDir stays false until session/workspace storage management
	// exists (see the Remove doc on the Manager interface).
	return RemoveResult{ProjectID: id, RemovedStorageDir: false}, nil
}

// findByPath scans the active registry for a project at path. The project count
// is small, so a List scan beats adding a dedicated indexed query for now.
func (m *manager) findByPath(ctx context.Context, path string) (sqlite.ProjectRow, bool, error) {
	rows, err := m.store.ListProjects(ctx)
	if err != nil {
		return sqlite.ProjectRow{}, false, err
	}
	for _, r := range rows {
		if r.Path == path {
			return r, true, nil
		}
	}
	return sqlite.ProjectRow{}, false, nil
}

// suggestID returns the first "<base><n>" id that is not already registered.
func (m *manager) suggestID(ctx context.Context, base domain.ProjectID) domain.ProjectID {
	for i := 1; ; i++ {
		candidate := domain.ProjectID(string(base) + strconv.Itoa(i))
		if _, ok, _ := m.store.GetProject(ctx, string(candidate)); !ok {
			return candidate
		}
	}
}

// --- pure helpers (registry → wire mapping, path/id validation) -------------

func projectFromRow(row sqlite.ProjectRow) Project {
	return Project{
		ID:            domain.ProjectID(row.ID),
		Name:          displayName(row),
		Path:          row.Path,
		Repo:          row.RepoOriginURL,
		DefaultBranch: "main",
	}
}

func displayName(row sqlite.ProjectRow) string {
	if strings.TrimSpace(row.DisplayName) != "" {
		return row.DisplayName
	}
	return row.ID
}

// normalizePath trims, ~-expands, and absolutizes a repository path.
func normalizePath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", httpx.BadRequest("PATH_REQUIRED", "Repository path is required", nil)
	}
	if strings.HasPrefix(raw, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", httpx.BadRequest("INVALID_PATH", "Repository path could not be expanded", nil)
		}
		switch {
		case raw == "~":
			raw = home
		case strings.HasPrefix(raw, "~/") || strings.HasPrefix(raw, `~\`):
			raw = filepath.Join(home, raw[2:])
		}
	}
	abs, err := filepath.Abs(raw)
	if err != nil {
		return "", httpx.BadRequest("INVALID_PATH", "Repository path is invalid", nil)
	}
	return filepath.Clean(abs), nil
}

// isGitRepo reports whether path is inside a git work tree whose top level is
// path itself (a registered project must be a repo root, not a subdirectory).
func isGitRepo(path string) bool {
	out, err := exec.Command("git", "-C", path, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return false
	}
	top, err := filepath.EvalSymlinks(filepath.Clean(strings.TrimSpace(string(out))))
	if err != nil {
		return false
	}
	p, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return false
	}
	return strings.EqualFold(top, p)
}

func defaultProjectID(path string) domain.ProjectID {
	id := strings.ToLower(strings.TrimSpace(filepath.Base(path)))
	id = strings.ReplaceAll(id, " ", "-")
	return domain.ProjectID(id)
}

var projectIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func validateProjectID(id domain.ProjectID) error {
	raw := string(id)
	if raw == "" || raw == "." || raw == ".." || strings.ContainsAny(raw, `/\`) || !projectIDPattern.MatchString(raw) {
		return httpx.BadRequest("INVALID_PROJECT_ID", "Project id failed storage-path validation", nil)
	}
	return nil
}

func sessionPrefix(id string) string {
	switch {
	case id == "":
		return "ao"
	case len(id) <= 12:
		return id
	default:
		return id[:12]
	}
}
