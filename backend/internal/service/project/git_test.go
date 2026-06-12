package project_test

import (
	"context"
	"runtime"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/service/project"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// fakeGitChecker is a GitChecker that answers from an in-memory set of repo
// paths — no git binary, no working tree. It is the seam that lets the project
// service be exercised in a unit test.
type fakeGitChecker struct{ repos map[string]bool }

func (f fakeGitChecker) IsRepo(path string) bool { return f.repos[path] }

func newManagerWithGit(t *testing.T, git project.GitChecker) project.Manager {
	t.Helper()
	store, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return project.NewWithGitChecker(store, git)
}

func TestAdd_UsesInjectedGitChecker(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	m := newManagerWithGit(t, fakeGitChecker{repos: map[string]bool{dir: true}})

	// A path the fake recognizes as a repo is accepted — without shelling out to git.
	if _, err := m.Add(ctx, project.AddInput{Path: dir, ProjectID: ptr("ao")}); err != nil {
		t.Fatalf("Add on a fake-recognized repo: %v", err)
	}
}

func TestAdd_RejectsNonRepoViaGitChecker(t *testing.T) {
	ctx := context.Background()
	m := newManagerWithGit(t, fakeGitChecker{repos: map[string]bool{}})

	_, err := m.Add(ctx, project.AddInput{Path: t.TempDir(), ProjectID: ptr("ao")})
	wantCode(t, err, "NOT_A_GIT_REPO")
}

// TestSamePath_CaseSensitivity guards the isGitRepo fix: paths differing only in
// case must be treated as distinct on case-sensitive filesystems (Linux) and as
// equal on the conventionally case-insensitive ones (macOS, Windows).
func TestSamePath_CaseSensitivity(t *testing.T) {
	// Document the platform contract so a regression in samePath is caught here.
	caseInsensitive := runtime.GOOS == "darwin" || runtime.GOOS == "windows"
	if got := project.SamePathForTest("/a/Repo", "/a/repo"); got != caseInsensitive {
		t.Fatalf("samePath(/a/Repo, /a/repo) = %v on %s, want %v", got, runtime.GOOS, caseInsensitive)
	}
	if !project.SamePathForTest("/a/repo", "/a/repo") {
		t.Fatalf("samePath of identical paths must be true")
	}
}
