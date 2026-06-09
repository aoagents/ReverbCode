package project_test

import (
	"context"
	"errors"
	"os/exec"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/project"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// newManager builds a Manager over a real, throwaway sqlite store (pure-Go
// driver, migrations run on Open) — no in-memory store.
func newManager(t *testing.T) project.Manager {
	t.Helper()
	store, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return project.New(store)
}

// gitRepo creates a real git repository in a fresh temp dir and returns its path.
func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", dir).CombinedOutput(); err != nil {
		t.Fatalf("git unavailable: %v (%s)", err, out)
	}
	return dir
}

func ptr(s string) *string { return &s }

// wantCode asserts err is an *apierr.Error carrying the given machine code.
func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	var e *apierr.Error
	if !errors.As(err, &e) {
		t.Fatalf("error = %v, want *apierr.Error", err)
	}
	if e.Code != code {
		t.Fatalf("code = %q, want %q", e.Code, code)
	}
}

func TestManager_AddListGetRemove(t *testing.T) {
	ctx := context.Background()
	m := newManager(t)
	repo := gitRepo(t)

	if got, err := m.List(ctx); err != nil || len(got) != 0 {
		t.Fatalf("List() = %v, %v; want empty", got, err)
	}

	proj, err := m.Add(ctx, project.AddInput{Path: repo, ProjectID: ptr("ao"), Name: ptr("Agent Orchestrator")})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if proj.ID != "ao" || proj.Name != "Agent Orchestrator" || proj.Path != repo || proj.DefaultBranch != "main" {
		t.Fatalf("Add returned %#v", proj)
	}

	list, err := m.List(ctx)
	if err != nil || len(list) != 1 || list[0].ID != "ao" {
		t.Fatalf("List() = %v, %v; want [ao]", list, err)
	}

	res, err := m.Get(ctx, "ao")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if res.Status != "ok" || res.Project == nil || res.Project.ID != "ao" {
		t.Fatalf("Get = %#v", res)
	}

	rm, err := m.Remove(ctx, "ao")
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if rm.ProjectID != "ao" || rm.RemovedStorageDir {
		t.Fatalf("Remove = %#v", rm)
	}
	if list, _ := m.List(ctx); len(list) != 0 {
		t.Fatalf("active list after remove = %d, want 0", len(list))
	}
	_, err = m.Get(ctx, "ao")
	wantCode(t, err, "PROJECT_NOT_FOUND")

	_, err = m.Remove(ctx, "ao")
	wantCode(t, err, "PROJECT_NOT_FOUND")
}

func TestManager_DefaultsWhenUnconfigured(t *testing.T) {
	ctx := context.Background()
	m := newManager(t)
	repo := gitRepo(t)

	if _, err := m.Add(ctx, project.AddInput{Path: repo, ProjectID: ptr("ao")}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Get on a project that set no config still reports the default branch and a
	// derived session prefix, and omits the (empty) config object.
	got, err := m.Get(ctx, "ao")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Project == nil {
		t.Fatalf("Get returned no project: %#v", got)
	}
	if got.Project.DefaultBranch != domain.DefaultBranchName {
		t.Fatalf("default branch = %q, want %q", got.Project.DefaultBranch, domain.DefaultBranchName)
	}
	if got.Project.Config != nil {
		t.Fatalf("unconfigured project should omit config, got %#v", got.Project.Config)
	}

	list, err := m.List(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("List = %v, %v", list, err)
	}
	if list[0].SessionPrefix != "ao" {
		t.Fatalf("default session prefix = %q, want derived 'ao'", list[0].SessionPrefix)
	}
}

func TestManager_SetConfig(t *testing.T) {
	ctx := context.Background()
	m := newManager(t)
	repo := gitRepo(t)

	if _, err := m.Add(ctx, project.AddInput{Path: repo, ProjectID: ptr("ao")}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	cfg := domain.ProjectConfig{
		DefaultBranch: "develop",
		Env:           map[string]string{"FOO": "bar"},
		AgentConfig:   domain.AgentConfig{Model: "claude-opus-4-5"},
	}
	proj, err := m.SetConfig(ctx, "ao", project.SetConfigInput{Config: cfg})
	if err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	if proj.Config == nil || proj.Config.AgentConfig.Model != "claude-opus-4-5" {
		t.Fatalf("returned config = %#v", proj.Config)
	}
	if proj.DefaultBranch != "develop" {
		t.Fatalf("DefaultBranch = %q, want develop", proj.DefaultBranch)
	}

	// The config persists and shows up on a fresh Get.
	got, err := m.Get(ctx, "ao")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Project == nil || got.Project.Config == nil || got.Project.Config.Env["FOO"] != "bar" {
		t.Fatalf("Get config = %#v", got.Project)
	}

	// An invalid permission value is rejected when set.
	_, err = m.SetConfig(ctx, "ao", project.SetConfigInput{Config: domain.ProjectConfig{AgentConfig: domain.AgentConfig{Permissions: "yolo"}}})
	wantCode(t, err, "INVALID_PROJECT_CONFIG")

	// An unknown role-override harness is rejected too.
	_, err = m.SetConfig(ctx, "ao", project.SetConfigInput{Config: domain.ProjectConfig{Worker: domain.RoleOverride{Harness: "nope"}}})
	wantCode(t, err, "INVALID_PROJECT_CONFIG")

	// Setting on an unknown project is a clean not-found.
	_, err = m.SetConfig(ctx, "ghost", project.SetConfigInput{Config: cfg})
	wantCode(t, err, "PROJECT_NOT_FOUND")
}

func TestManager_ReaddAfterRemove(t *testing.T) {
	ctx := context.Background()
	m := newManager(t)
	repo := gitRepo(t)

	if _, err := m.Add(ctx, project.AddInput{Path: repo, ProjectID: ptr("ao")}); err != nil {
		t.Fatalf("first Add: %v", err)
	}
	if _, err := m.Remove(ctx, "ao"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := m.Add(ctx, project.AddInput{Path: repo, ProjectID: ptr("ao2")}); err != nil {
		t.Fatalf("re-add same path after remove: %v", err)
	}

	otherRepo := gitRepo(t)
	if _, err := m.Remove(ctx, "ao2"); err != nil {
		t.Fatalf("Remove ao2: %v", err)
	}
	if _, err := m.Add(ctx, project.AddInput{Path: otherRepo, ProjectID: ptr("ao2")}); err != nil {
		t.Fatalf("re-add same id at different path after remove: %v", err)
	}
}

func TestManager_AddValidationAndConflicts(t *testing.T) {
	ctx := context.Background()
	m := newManager(t)

	_, err := m.Add(ctx, project.AddInput{Path: ""})
	wantCode(t, err, "PATH_REQUIRED")

	_, err = m.Add(ctx, project.AddInput{Path: t.TempDir()}) // exists but not a git repo
	wantCode(t, err, "NOT_A_GIT_REPO")

	// An embedded ".." passes the id pattern but would yield an invalid git
	// branch (ao/a..b-1) at spawn time; reject it up front as a clear 400.
	_, err = m.Add(ctx, project.AddInput{Path: gitRepo(t), ProjectID: ptr("a..b")})
	wantCode(t, err, "INVALID_PROJECT_ID")

	repoA, repoB := gitRepo(t), gitRepo(t)
	if _, err := m.Add(ctx, project.AddInput{Path: repoA, ProjectID: ptr("shared")}); err != nil {
		t.Fatalf("seed add: %v", err)
	}
	_, err = m.Add(ctx, project.AddInput{Path: repoA, ProjectID: ptr("other")})
	wantCode(t, err, "PATH_ALREADY_REGISTERED")

	_, err = m.Add(ctx, project.AddInput{Path: repoB, ProjectID: ptr("shared")})
	wantCode(t, err, "ID_ALREADY_REGISTERED")
}

// gitRepoWithOrigin creates a real git repo with an `origin` remote pointing
// at `originURL`. Used to assert project.Add captures the origin at add time.
func gitRepoWithOrigin(t *testing.T, originURL string) string {
	t.Helper()
	dir := gitRepo(t)
	if out, err := exec.Command("git", "-C", dir, "remote", "add", "origin", originURL).CombinedOutput(); err != nil {
		t.Fatalf("git remote add: %v (%s)", err, out)
	}
	return dir
}

func TestManager_AddPopulatesRepoOriginURL(t *testing.T) {
	ctx := context.Background()

	for _, tc := range []struct {
		name    string
		setup   func(t *testing.T) string
		wantURL string
	}{
		{
			name:    "git repo with origin populates url",
			setup:   func(t *testing.T) string { return gitRepoWithOrigin(t, "https://github.com/o/r.git") },
			wantURL: "https://github.com/o/r.git",
		},
		{
			name:    "git repo without origin leaves url empty",
			setup:   func(t *testing.T) string { return gitRepo(t) },
			wantURL: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newManager(t)
			path := tc.setup(t)
			proj, err := m.Add(ctx, project.AddInput{Path: path, ProjectID: ptr("p")})
			if err != nil {
				t.Fatalf("Add: %v", err)
			}
			if proj.Repo != tc.wantURL {
				t.Fatalf("Repo = %q, want %q", proj.Repo, tc.wantURL)
			}
		})
	}
}

func TestManager_GetUpdateRemoveErrors(t *testing.T) {
	ctx := context.Background()
	m := newManager(t)

	_, err := m.Get(ctx, "nope")
	wantCode(t, err, "PROJECT_NOT_FOUND")

	_, err = m.Get(ctx, domain.ProjectID("bad/id"))
	wantCode(t, err, "INVALID_PROJECT_ID")

	_, err = m.Remove(ctx, "nope")
	wantCode(t, err, "PROJECT_NOT_FOUND")

	repo := gitRepo(t)
	if _, err := m.Add(ctx, project.AddInput{Path: repo, ProjectID: ptr("p")}); err != nil {
		t.Fatalf("seed: %v", err)
	}
}
