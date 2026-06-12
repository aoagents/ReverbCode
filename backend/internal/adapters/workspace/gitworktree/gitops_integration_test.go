package gitworktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestGitOpsIntegrationStatusCommitPush(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	runGit(t, git, repo, "config", "user.email", "ao@example.com")
	runGit(t, git, repo, "config", "user.name", "Ao Agents")

	ops := NewGitOps()
	ctx := context.Background()

	// Clean workspace: branch resolved, no files, commit refuses.
	status, err := ops.Status(ctx, repo)
	if err != nil {
		t.Fatalf("status clean: %v", err)
	}
	if status.Branch != "main" || len(status.Files) != 0 {
		t.Fatalf("clean status = %+v", status)
	}
	if _, err := ops.CommitAll(ctx, repo, "noop", false); !errors.Is(err, ports.ErrGitNothingToCommit) {
		t.Fatalf("commit on clean tree = %v, want ErrGitNothingToCommit", err)
	}

	// Tracked edit + untracked file show up with line counts and staged flags.
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("seed\nmore\n"), 0o644); err != nil {
		t.Fatalf("edit README: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "notes.txt"), []byte("a\nb\nc\n"), 0o644); err != nil {
		t.Fatalf("write notes: %v", err)
	}
	status, err = ops.Status(ctx, repo)
	if err != nil {
		t.Fatalf("status dirty: %v", err)
	}
	if len(status.Files) != 2 {
		t.Fatalf("dirty files = %+v", status.Files)
	}
	byPath := map[string]ports.GitFileChange{}
	for _, f := range status.Files {
		byPath[f.Path] = f
	}
	if f := byPath["README.md"]; f.Additions != 1 || f.Staged {
		t.Fatalf("README change = %+v", f)
	}
	if f := byPath["notes.txt"]; f.Additions != 3 || f.Staged {
		t.Fatalf("notes change = %+v", f)
	}

	// StageAll flips the staged flag.
	if err := ops.StageAll(ctx, repo); err != nil {
		t.Fatalf("stage all: %v", err)
	}
	status, err = ops.Status(ctx, repo)
	if err != nil {
		t.Fatalf("status staged: %v", err)
	}
	for _, f := range status.Files {
		if !f.Staged {
			t.Fatalf("file not staged after StageAll: %+v", f)
		}
	}

	// CommitAll with push lands on the origin remote.
	result, err := ops.CommitAll(ctx, repo, "feat: rail test", true)
	if err != nil {
		t.Fatalf("commit+push: %v", err)
	}
	if result.SHA == "" || result.Branch != "main" || !result.Pushed {
		t.Fatalf("commit result = %+v", result)
	}
	status, err = ops.Status(ctx, repo)
	if err != nil {
		t.Fatalf("status after commit: %v", err)
	}
	if len(status.Files) != 0 {
		t.Fatalf("files after commit = %+v", status.Files)
	}
}

func TestGitOpsIntegrationDiscardAll(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)

	ops := NewGitOps()
	ctx := context.Background()

	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("clobbered\n"), 0o644); err != nil {
		t.Fatalf("edit README: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "junk.txt"), []byte("junk\n"), 0o644); err != nil {
		t.Fatalf("write junk: %v", err)
	}

	if err := ops.DiscardAll(ctx, repo); err != nil {
		t.Fatalf("discard: %v", err)
	}
	status, err := ops.Status(ctx, repo)
	if err != nil {
		t.Fatalf("status after discard: %v", err)
	}
	if len(status.Files) != 0 {
		t.Fatalf("files after discard = %+v", status.Files)
	}
	contents, err := os.ReadFile(filepath.Join(repo, "README.md"))
	if err != nil || string(contents) != "seed\n" {
		t.Fatalf("README after discard = %q, %v", contents, err)
	}
	if _, err := os.Stat(filepath.Join(repo, "junk.txt")); !os.IsNotExist(err) {
		t.Fatalf("junk.txt still present after discard: %v", err)
	}
}

func TestGitOpsIntegrationCommitPushWithoutRemote(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "local")
	run(t, git, "init", repo)
	runGit(t, git, repo, "config", "user.email", "ao@example.com")
	runGit(t, git, repo, "config", "user.name", "Ao Agents")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	runGit(t, git, repo, "add", "-A")
	runGit(t, git, repo, "commit", "-m", "seed")

	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a\nb\n"), 0o644); err != nil {
		t.Fatalf("edit: %v", err)
	}

	ops := NewGitOps()
	result, err := ops.CommitAll(context.Background(), repo, "feat: local", true)
	if !errors.Is(err, ports.ErrGitNoRemote) {
		t.Fatalf("push without remote = %v, want ErrGitNoRemote", err)
	}
	// The commit itself landed; only the push was impossible.
	if result.SHA == "" || result.Pushed {
		t.Fatalf("result = %+v", result)
	}
}
