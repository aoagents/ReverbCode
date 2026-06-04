package gitworktree

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestWorkspaceIntegrationCreateRestoreDestroy(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	root := filepath.Join(tmp, "managed")
	ws, err := New(Options{Binary: git, ManagedRoot: root, RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	cfg := ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess", Branch: "feature/one"}

	info, err := ws.Create(ctx, cfg)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if info.Path != filepath.Join(ws.managedRoot, "proj", "sess") || info.Branch != cfg.Branch || info.SessionID != cfg.SessionID || info.ProjectID != cfg.ProjectID {
		t.Fatalf("info = %#v", info)
	}
	if _, err := os.Stat(filepath.Join(info.Path, "README.md")); err != nil {
		t.Fatalf("created worktree missing seed file: %v", err)
	}

	restored, err := ws.Restore(ctx, cfg)
	if err != nil {
		t.Fatalf("restore registered: %v", err)
	}
	if restored.Path != info.Path || restored.Branch != cfg.Branch {
		t.Fatalf("restored = %#v", restored)
	}

	if err := ws.Destroy(ctx, info); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	if _, err := os.Stat(info.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("path after destroy stat err = %v, want not exist", err)
	}

	restored, err = ws.Restore(ctx, cfg)
	if err != nil {
		t.Fatalf("restore after destroy: %v", err)
	}
	if restored.Path != info.Path || restored.Branch != cfg.Branch {
		t.Fatalf("restored after destroy = %#v", restored)
	}
	if err := ws.Destroy(ctx, restored); err != nil {
		t.Fatalf("destroy restored: %v", err)
	}
}

func TestWorkspaceIntegrationDestroyRefusesLockedWorktree(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	root := filepath.Join(tmp, "managed")
	ws, err := New(Options{Binary: git, ManagedRoot: root, RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	info, err := ws.Create(ctx, ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess", Branch: "feature/lock"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	runGit(t, git, repo, "worktree", "lock", info.Path)

	err = ws.Destroy(ctx, info)
	if err == nil || !strings.Contains(err.Error(), "still registered") {
		t.Fatalf("destroy locked error = %v, want still registered refusal", err)
	}
	if _, statErr := os.Stat(filepath.Join(info.Path, "README.md")); statErr != nil {
		t.Fatalf("locked worktree was not preserved: %v", statErr)
	}

	runGit(t, git, repo, "worktree", "unlock", info.Path)
	if err := ws.Destroy(ctx, info); err != nil {
		t.Fatalf("destroy after unlock: %v", err)
	}
}

func TestWorkspaceIntegrationCreateFetchesLatestRemote(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	root := filepath.Join(tmp, "managed")
	ws, err := New(Options{Binary: git, ManagedRoot: root, RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	// Push a new commit to origin/main FROM a separate pusher clone, bypassing
	// the working clone. Without fetch, the working clone wouldn't see it.
	origin := filepath.Join(tmp, "origin.git")
	pusher := filepath.Join(tmp, "pusher")
	run(t, git, "clone", origin, pusher)
	// Mirror setupOriginClone's explicit `checkout main`: on CI runners where
	// `init.defaultBranch` is `master`, the bare `origin.git` HEAD still points
	// at refs/heads/master after the seed pushed main, so a fresh `git clone`
	// here checks out HEAD (master, which doesn't exist) and lands the pusher
	// on no branch.
	runGit(t, git, pusher, "checkout", "main")
	runGit(t, git, pusher, "config", "user.email", "ao@example.com")
	runGit(t, git, pusher, "config", "user.name", "Ao Agents")
	if err := os.WriteFile(filepath.Join(pusher, "post-seed.txt"), []byte("after seed\n"), 0o644); err != nil {
		t.Fatalf("write post-seed: %v", err)
	}
	runGit(t, git, pusher, "add", "post-seed.txt")
	runGit(t, git, pusher, "commit", "-m", "post-seed")
	runGit(t, git, pusher, "push", "origin", "main")

	info, err := ws.Create(context.Background(), ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess", Branch: "feature/fetch"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := os.Stat(filepath.Join(info.Path, "post-seed.txt")); err != nil {
		t.Fatalf("expected post-seed.txt in worktree (proves fetch ran): %v", err)
	}
}

func requireGit(t *testing.T) string {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not found")
	}
	return git
}

func setupOriginClone(t *testing.T, git, tmp string) string {
	t.Helper()
	origin := filepath.Join(tmp, "origin.git")
	seed := filepath.Join(tmp, "seed")
	repo := filepath.Join(tmp, "repo")
	run(t, git, "init", "--bare", origin)
	run(t, git, "init", seed)
	runGit(t, git, seed, "config", "user.email", "ao@example.com")
	runGit(t, git, seed, "config", "user.name", "Ao Agents")
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("seed\n"), 0o644); err != nil {
		t.Fatalf("write seed: %v", err)
	}
	runGit(t, git, seed, "add", "README.md")
	runGit(t, git, seed, "commit", "-m", "seed")
	runGit(t, git, seed, "branch", "-M", "main")
	runGit(t, git, seed, "remote", "add", "origin", origin)
	runGit(t, git, seed, "push", "-u", "origin", "main")
	run(t, git, "clone", origin, repo)
	runGit(t, git, repo, "checkout", "main")
	return repo
}

func runGit(t *testing.T, git, dir string, args ...string) {
	t.Helper()
	run(t, git, append([]string{"-C", dir}, args...)...)
}

func run(t *testing.T, binary string, args ...string) {
	t.Helper()
	cmd := exec.Command(binary, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", binary, strings.Join(args, " "), err, out)
	}
}
