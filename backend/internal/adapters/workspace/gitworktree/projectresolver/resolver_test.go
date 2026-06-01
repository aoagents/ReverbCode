package projectresolver_test

import (
	"context"
	"os/exec"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/workspace/gitworktree"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/workspace/gitworktree/projectresolver"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/project"
)

func TestSatisfiesRepoResolver(t *testing.T) {
	var _ gitworktree.RepoResolver = (*projectresolver.Resolver)(nil)
}

func TestRepoPath_ReturnsProjectPath(t *testing.T) {
	mgr := project.NewMemoryManager()
	repo := mkGitRepo(t)
	added, err := mgr.Add(context.Background(), project.AddInput{Path: repo})
	if err != nil {
		t.Fatal(err)
	}
	r := projectresolver.New(mgr)
	got, err := r.RepoPath(added.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got != added.Path {
		t.Fatalf("got %q want %q", got, added.Path)
	}
}

func TestRepoPath_UnknownProjectReturnsError(t *testing.T) {
	mgr := project.NewMemoryManager()
	r := projectresolver.New(mgr)
	if _, err := r.RepoPath("nope"); err == nil {
		t.Fatal("expected error for unknown project")
	}
}

func TestRepoPath_DegradedProjectReturnsError(t *testing.T) {
	// Degraded resolves a status, not a Project — the resolver must surface an
	// error rather than the empty path that would silently mis-create worktrees.
	r := projectresolver.New(stubManagerDegraded{})
	_, err := r.RepoPath("p1")
	if err == nil {
		t.Fatal("expected error for degraded project")
	}
}

// stubManagerDegraded only overrides Get; other Manager methods would panic if
// reached, which they should not in this test.
type stubManagerDegraded struct{ project.Manager }

func (stubManagerDegraded) Get(context.Context, domain.ProjectID) (project.GetResult, error) {
	return project.GetResult{Status: "degraded"}, nil
}

func mkGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-q", dir)
	if err := cmd.Run(); err != nil {
		t.Skipf("git not available: %v", err)
	}
	return dir
}
