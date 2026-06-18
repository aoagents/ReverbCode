package legacyimport

import (
	"os"
	"path/filepath"
	"testing"

	yaml "gopkg.in/yaml.v3"
)

func nonNilNode() *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Value: "x"} }

func TestClaudeSlug(t *testing.T) {
	if got := claudeSlug("/Users/me/Code/proj.x"); got != "-Users-me-Code-proj-x" {
		t.Fatalf("slug = %q", got)
	}
}

func TestPlanTranscriptCopy_DestUsesOrchestratorTemplate(t *testing.T) {
	plan := planTranscriptCopy("/data", "proj", "pre", "/legacy/wt", "uuid-1", "/claude")
	// Destination slug = slug({dataDir}/worktrees/{projectID}/orchestrator/{prefix}-orchestrator).
	wantDest := filepath.Join("/claude", claudeSlug("/data/worktrees/proj/orchestrator/pre-orchestrator"), "uuid-1.jsonl")
	if plan.destPath != wantDest {
		t.Fatalf("destPath = %q, want %q", plan.destPath, wantDest)
	}
}

func TestRelocateTranscript_CopiesAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	claudeDir := filepath.Join(dir, "claude")
	worktree := filepath.Join(dir, "wt")
	if err := os.MkdirAll(worktree, 0o750); err != nil {
		t.Fatal(err)
	}
	// Seed the legacy transcript at the source slug. planTranscriptCopy
	// realpath-resolves the worktree, so seed under the resolved slug (matters on
	// macOS where /var/folders is a symlink to /private/var/folders).
	resolvedWt, err := filepath.EvalSymlinks(worktree)
	if err != nil {
		t.Fatal(err)
	}
	srcSlug := claudeSlug(resolvedWt)
	srcDir := filepath.Join(claudeDir, srcSlug)
	if err := os.MkdirAll(srcDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "uuid-1.jsonl"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}

	plan := planTranscriptCopy(filepath.Join(dir, "data"), "proj", "pre", worktree, "uuid-1", claudeDir)
	out, err := relocateTranscript(plan)
	if err != nil || out != transcriptCopied {
		t.Fatalf("relocate = (%s,%v), want copied", out, err)
	}
	if b, err := os.ReadFile(plan.destPath); err != nil || string(b) != "hello" {
		t.Fatalf("dest content = %q err=%v", b, err)
	}
	// Re-run: destination already present.
	if out, _ := relocateTranscript(plan); out != transcriptAlreadyPresent {
		t.Fatalf("second relocate = %s, want already-present", out)
	}
}

func TestPlanTranscriptCopy_DestResolvesSymlinkedDataDir(t *testing.T) {
	// The daemon resolves the orchestrator worktree through physicalAbs before
	// cd-ing into it, so the dest slug must use the symlink-resolved data dir —
	// not the literal one — or `claude --resume` misses the bucket.
	realData := t.TempDir()
	linkDir := filepath.Join(t.TempDir(), "data-link")
	if err := os.Symlink(realData, linkDir); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	plan := planTranscriptCopy(linkDir, "proj", "pre", "/legacy/wt", "uuid-1", "/claude")

	resolvedReal, err := filepath.EvalSymlinks(realData)
	if err != nil {
		t.Fatal(err)
	}
	wantSlug := claudeSlug(filepath.Join(resolvedReal, "worktrees", "proj", "orchestrator", "pre-orchestrator"))
	wantDest := filepath.Join("/claude", wantSlug, "uuid-1.jsonl")
	if plan.destPath != wantDest {
		t.Fatalf("destPath = %q,\n want %q (resolved, not the symlinked %q)", plan.destPath, wantDest, linkDir)
	}
}

func TestRelocateTranscript_SourceMissing(t *testing.T) {
	plan := planTranscriptCopy(t.TempDir(), "proj", "pre", "/nope/wt", "uuid-x", filepath.Join(t.TempDir(), "claude"))
	if out, err := relocateTranscript(plan); err != nil || out != transcriptSourceMissing {
		t.Fatalf("relocate = (%s,%v), want source-missing", out, err)
	}
}
