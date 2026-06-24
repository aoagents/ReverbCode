package gitworktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// TestWorkspaceIntegrationStashApplyRoundTrip is the primary correctness test
// for the save-on-close / restore-on-open lifecycle:
//
//  1. Create a worktree with a tracked-file edit, a new non-ignored file,
//     and a file covered by .gitignore.
//  2. StashUncommitted: assert the returned ref is non-empty.
//  3. ForceDestroy: remove the worktree unconditionally.
//  4. Re-add the worktree via Restore (simulating the re-open path).
//  5. ApplyPreserved: replay the captured state.
//  6. Assert that the tracked edit and the new non-ignored file reappear,
//     and the .gitignore-matched file does NOT reappear.
func TestWorkspaceIntegrationStashApplyRoundTrip(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	root := filepath.Join(tmp, "managed")
	ws, err := New(Options{Binary: git, ManagedRoot: root, RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	cfg := ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess-preserve", Branch: "feature/preserve"}

	info, err := ws.Create(ctx, cfg)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Stage 1: create a .gitignore that covers a secret file.
	if err := os.WriteFile(filepath.Join(info.Path, ".gitignore"), []byte("secret.txt\n"), 0o644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}
	runGit(t, git, info.Path, "add", ".gitignore")
	runGit(t, git, info.Path, "commit", "-m", "add gitignore")

	// Stage 2: create uncommitted work:
	//   - tracked-file edit: modify README.md (already committed from seed)
	if err := os.WriteFile(filepath.Join(info.Path, "README.md"), []byte("edited by agent\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	//   - new non-ignored file: should be captured
	if err := os.WriteFile(filepath.Join(info.Path, "agent-work.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write agent-work.go: %v", err)
	}
	//   - ignored file: must NOT be captured
	if err := os.WriteFile(filepath.Join(info.Path, "secret.txt"), []byte("super-secret\n"), 0o644); err != nil {
		t.Fatalf("write secret.txt: %v", err)
	}

	// StashUncommitted: must return a non-empty ref.
	ref, err := ws.StashUncommitted(ctx, info)
	if err != nil {
		t.Fatalf("StashUncommitted: %v", err)
	}
	if ref == "" {
		t.Fatal("StashUncommitted returned empty ref for dirty worktree")
	}
	if !strings.HasPrefix(ref, "refs/ao/preserved/") {
		t.Fatalf("ref = %q, want refs/ao/preserved/... prefix", ref)
	}

	// ForceDestroy: simulate session close.
	if err := ws.ForceDestroy(ctx, info); err != nil {
		t.Fatalf("ForceDestroy: %v", err)
	}
	if _, err := os.Stat(info.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("worktree path still exists after ForceDestroy")
	}

	// Restore: simulate re-open / re-attach.
	restored, err := ws.Restore(ctx, cfg)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if restored.Path != info.Path {
		t.Fatalf("restored path = %q, want %q", restored.Path, info.Path)
	}

	// ApplyPreserved: replay the captured state.
	if err := ws.ApplyPreserved(ctx, restored, ref); err != nil {
		t.Fatalf("ApplyPreserved: %v", err)
	}

	// Tracked edit must reappear.
	readmeBytes, err := os.ReadFile(filepath.Join(restored.Path, "README.md"))
	if err != nil {
		t.Fatalf("read README after apply: %v", err)
	}
	if string(readmeBytes) != "edited by agent\n" {
		t.Fatalf("README content = %q, want %q", string(readmeBytes), "edited by agent\n")
	}

	// New non-ignored file must reappear.
	if _, err := os.Stat(filepath.Join(restored.Path, "agent-work.go")); err != nil {
		t.Fatalf("agent-work.go missing after apply: %v", err)
	}

	// Ignored file must NOT reappear.
	if _, err := os.Stat(filepath.Join(restored.Path, "secret.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("secret.txt exists after apply but must not (it was .gitignore-d)")
	}

	// After a successful apply the ref must be deleted.
	checkRefArgs := revParseVerifyArgs(repo, ref)
	if out, err := ws.run(ctx, ws.binary, checkRefArgs...); err == nil {
		t.Fatalf("preserve ref %q still exists after successful ApplyPreserved (points to %s)", ref, strings.TrimSpace(string(out)))
	}
}

// TestWorkspaceIntegrationStashCleanWorktree proves that StashUncommitted on a
// clean worktree returns an empty ref and no error (nothing to preserve).
func TestWorkspaceIntegrationStashCleanWorktree(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	root := filepath.Join(tmp, "managed")
	ws, err := New(Options{Binary: git, ManagedRoot: root, RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	cfg := ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess-clean", Branch: "feature/clean-stash"}

	info, err := ws.Create(ctx, cfg)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	ref, err := ws.StashUncommitted(ctx, info)
	if err != nil {
		t.Fatalf("StashUncommitted on clean worktree: %v", err)
	}
	if ref != "" {
		t.Fatalf("StashUncommitted on clean worktree returned non-empty ref %q, want empty", ref)
	}

	// Cleanup.
	if err := ws.Destroy(ctx, info); err != nil {
		t.Fatalf("destroy clean worktree: %v", err)
	}
}
