package project_test

import (
	"context"
	"os/exec"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/httpx"
	"github.com/aoagents/agent-orchestrator/backend/internal/project"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// newManager builds a Manager over a real, throwaway sqlite store (pure-Go
// driver, migrations run on Open) — no fake, no in-memory store.
func newManager(t *testing.T) project.Manager {
	t.Helper()
	store, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return project.NewManager(store)
}

// gitRepo creates a real git repository in a fresh temp dir and returns its path.
func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", dir).CombinedOutput(); err != nil {
		t.Skipf("git unavailable: %v (%s)", err, out)
	}
	return dir
}

func ptr(s string) *string { return &s }

// wantAPIErr asserts err is an httpx.APIErr with the given status + code.
func wantAPIErr(t *testing.T, err error, status int, code string) {
	t.Helper()
	e, ok := httpx.AsAPIErr(err)
	if !ok {
		t.Fatalf("error = %v, want *httpx.APIErr", err)
	}
	if e.Status != status || e.Code != code {
		t.Fatalf("error = %d/%s, want %d/%s", e.Status, e.Code, status, code)
	}
}

func TestManager_AddListGetRemove(t *testing.T) {
	ctx := context.Background()
	m := newManager(t)
	repo := gitRepo(t)

	// empty list
	if got, err := m.List(ctx); err != nil || len(got) != 0 {
		t.Fatalf("List() = %v, %v; want empty", got, err)
	}

	// add
	proj, err := m.Add(ctx, project.AddInput{Path: repo, ProjectID: ptr("ao"), Name: ptr("Agent Orchestrator")})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if proj.ID != "ao" || proj.Name != "Agent Orchestrator" || proj.Path != repo || proj.DefaultBranch != "main" {
		t.Fatalf("Add returned %#v", proj)
	}

	// list now has it, with derived sessionPrefix
	list, err := m.List(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("List() = %v, %v; want 1", list, err)
	}
	if list[0].ID != "ao" || list[0].SessionPrefix != "ao" {
		t.Fatalf("summary = %#v", list[0])
	}

	// get → ok
	res, err := m.Get(ctx, "ao")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if res.Status != "ok" || res.Project == nil || res.Project.ID != "ao" {
		t.Fatalf("Get = %#v", res)
	}

	// remove → archived, drops out of List but Get still resolves the row
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
}

func TestManager_AddValidationAndConflicts(t *testing.T) {
	ctx := context.Background()
	m := newManager(t)

	_, err := m.Add(ctx, project.AddInput{Path: ""})
	wantAPIErr(t, err, 400, "PATH_REQUIRED")

	_, err = m.Add(ctx, project.AddInput{Path: t.TempDir()}) // dir exists but not a git repo
	wantAPIErr(t, err, 400, "NOT_A_GIT_REPO")

	repoA, repoB := gitRepo(t), gitRepo(t)
	if _, err := m.Add(ctx, project.AddInput{Path: repoA, ProjectID: ptr("shared")}); err != nil {
		t.Fatalf("seed add: %v", err)
	}

	// same path, different id → PATH_ALREADY_REGISTERED
	_, err = m.Add(ctx, project.AddInput{Path: repoA, ProjectID: ptr("other")})
	wantAPIErr(t, err, 409, "PATH_ALREADY_REGISTERED")

	// same id, different path → ID_ALREADY_REGISTERED
	_, err = m.Add(ctx, project.AddInput{Path: repoB, ProjectID: ptr("shared")})
	wantAPIErr(t, err, 409, "ID_ALREADY_REGISTERED")
}

func TestManager_GetUpdateErrors(t *testing.T) {
	ctx := context.Background()
	m := newManager(t)

	_, err := m.Get(ctx, "nope")
	wantAPIErr(t, err, 404, "PROJECT_NOT_FOUND")

	_, err = m.Get(ctx, domain.ProjectID("bad/id"))
	wantAPIErr(t, err, 400, "INVALID_PROJECT_ID")

	_, err = m.Remove(ctx, "nope")
	wantAPIErr(t, err, 404, "PROJECT_NOT_FOUND")

	// registry-only: config patching is unavailable even for an existing project
	repo := gitRepo(t)
	if _, err := m.Add(ctx, project.AddInput{Path: repo, ProjectID: ptr("p")}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err = m.UpdateConfig(ctx, "p", project.UpdateConfigInput{})
	wantAPIErr(t, err, 409, "PROJECT_CONFIG_UNAVAILABLE")
}
