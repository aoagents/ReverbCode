package legacyimport

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// fakeStore is an in-memory Store with the importer's idempotency semantics.
type fakeStore struct {
	projects map[string]domain.ProjectRecord
	sessions map[domain.SessionID]domain.SessionRecord
}

func newFakeStore() *fakeStore {
	return &fakeStore{projects: map[string]domain.ProjectRecord{}, sessions: map[domain.SessionID]domain.SessionRecord{}}
}

func (f *fakeStore) GetProject(_ context.Context, id string) (domain.ProjectRecord, bool, error) {
	r, ok := f.projects[id]
	return r, ok, nil
}
func (f *fakeStore) UpsertProject(_ context.Context, r domain.ProjectRecord) error {
	f.projects[r.ID] = r
	return nil
}
func (f *fakeStore) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	r, ok := f.sessions[id]
	return r, ok, nil
}
func (f *fakeStore) ImportSession(_ context.Context, rec domain.SessionRecord, _ int64) (bool, error) {
	if _, ok := f.sessions[rec.ID]; ok {
		return false, nil
	}
	f.sessions[rec.ID] = rec
	return true, nil
}

// writeLegacyRoot builds a minimal legacy store: two projects, an importable
// claude-code orchestrator for alpha (with a seeded transcript), an aider
// orchestrator for beta (skipped). Returns the legacy root and the claude dir.
func writeLegacyRoot(t *testing.T) (root, claudeDir string) {
	t.Helper()
	root = filepath.Join(t.TempDir(), ".agent-orchestrator")
	claudeDir = filepath.Join(t.TempDir(), "claude")
	mustMkdir(t, filepath.Join(root, "projects", "alpha", "sessions"))
	mustMkdir(t, filepath.Join(root, "projects", "beta", "sessions"))

	mustWrite(t, filepath.Join(root, "config.yaml"), `projects:
  alpha:
    path: /repos/alpha
    name: Alpha
    defaultBranch: develop
  beta:
    path: /repos/beta
`)

	worktree := filepath.Join(t.TempDir(), "alpha-wt")
	mustMkdir(t, worktree)
	mustWrite(t, filepath.Join(root, "projects", "alpha", "sessions", "orchestrator.json"), `{
      "role": "orchestrator",
      "agent": "claude-code",
      "worktree": "`+worktree+`",
      "claudeSessionUuid": "uuid-alpha",
      "userPrompt": "go",
      "createdAt": "2026-01-01T00:00:00Z",
      "lifecycle": {"session": {"state": "working", "lastTransitionAt": "2026-01-02T00:00:00Z"}}
    }`)
	// Seed the transcript at the legacy source slug so relocation copies it
	// (resolve symlinks to match planTranscriptCopy's realpath of the worktree).
	resolvedWt, err := filepath.EvalSymlinks(worktree)
	if err != nil {
		t.Fatal(err)
	}
	srcDir := filepath.Join(claudeDir, claudeSlug(resolvedWt))
	mustMkdir(t, srcDir)
	mustWrite(t, filepath.Join(srcDir, "uuid-alpha.jsonl"), "transcript")

	mustWrite(t, filepath.Join(root, "projects", "beta", "sessions", "orchestrator.json"), `{
      "role": "orchestrator",
      "agent": "aider",
      "lifecycle": {"session": {"state": "working"}}
    }`)
	return root, claudeDir
}

func runOpts(root, claudeDir string) Options {
	return Options{
		Root:              root,
		DataDir:           filepath.Join(filepath.Dir(root), "data"),
		ClaudeProjectsDir: claudeDir,
		Now:               time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		RepoOriginURL:     func(string) string { return "" },
	}
}

func TestRun_EndToEnd(t *testing.T) {
	root, claudeDir := writeLegacyRoot(t)
	store := newFakeStore()
	ctx := context.Background()

	rep, err := Run(ctx, store, runOpts(root, claudeDir))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if rep.ProjectsImported != 2 {
		t.Fatalf("projectsImported = %d, want 2", rep.ProjectsImported)
	}
	if rep.OrchestratorsImported != 1 {
		t.Fatalf("orchestratorsImported = %d, want 1 (alpha)", rep.OrchestratorsImported)
	}
	if rep.OrchestratorsSkipped != 1 {
		t.Fatalf("orchestratorsSkipped = %d, want 1 (beta/aider)", rep.OrchestratorsSkipped)
	}
	if rep.TranscriptsRelocated != 1 {
		t.Fatalf("transcriptsRelocated = %d, want 1", rep.TranscriptsRelocated)
	}
	// The alpha orchestrator row landed verbatim.
	o, ok := store.sessions["alpha-orchestrator"]
	if !ok || o.Kind != domain.KindOrchestrator || o.Metadata.AgentSessionID != "uuid-alpha" {
		t.Fatalf("alpha orchestrator = %+v ok=%v", o, ok)
	}
	// develop branch survives into the config blob.
	if store.projects["alpha"].Config.DefaultBranch != "develop" {
		t.Fatalf("alpha config = %+v", store.projects["alpha"].Config)
	}
}

func TestRun_Idempotent(t *testing.T) {
	root, claudeDir := writeLegacyRoot(t)
	store := newFakeStore()
	ctx := context.Background()
	if _, err := Run(ctx, store, runOpts(root, claudeDir)); err != nil {
		t.Fatalf("first run: %v", err)
	}
	rep, err := Run(ctx, store, runOpts(root, claudeDir))
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if rep.ProjectsImported != 0 || rep.ProjectsSkipped != 2 {
		t.Fatalf("re-run projects: imported=%d skipped=%d, want 0/2", rep.ProjectsImported, rep.ProjectsSkipped)
	}
	if rep.OrchestratorsImported != 0 {
		t.Fatalf("re-run orchestratorsImported = %d, want 0", rep.OrchestratorsImported)
	}
}

func TestRun_DryRunWritesNothing(t *testing.T) {
	root, claudeDir := writeLegacyRoot(t)
	store := newFakeStore()
	opts := runOpts(root, claudeDir)
	opts.DryRun = true
	rep, err := Run(context.Background(), store, opts)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if rep.ProjectsImported != 2 || rep.OrchestratorsImported != 1 {
		t.Fatalf("dry-run plan = %+v", rep)
	}
	if len(store.projects) != 0 || len(store.sessions) != 0 {
		t.Fatal("dry run must not write to the store")
	}
}

func TestRun_NoLegacyData(t *testing.T) {
	root := filepath.Join(t.TempDir(), "empty")
	rep, err := Run(context.Background(), newFakeStore(), Options{Root: root})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if rep.ProjectsImported != 0 || len(rep.Notes) == 0 {
		t.Fatalf("expected empty import with a note, got %+v", rep)
	}
}

func TestHasLegacyData(t *testing.T) {
	root, _ := writeLegacyRoot(t)
	if !HasLegacyData(root) {
		t.Fatal("HasLegacyData = false, want true")
	}
	if HasLegacyData(filepath.Join(t.TempDir(), "nope")) {
		t.Fatal("HasLegacyData = true for missing root")
	}
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o750); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, p, content string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
