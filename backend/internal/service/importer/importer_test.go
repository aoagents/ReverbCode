package importer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// fakeStore satisfies importer.Store with the engine's idempotency semantics
// plus the project listing the service's detection probe reads.
type fakeStore struct {
	projects map[string]domain.ProjectRecord
	sessions map[domain.SessionID]domain.SessionRecord
	listErr  error
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
func (f *fakeStore) ListProjects(_ context.Context) ([]domain.ProjectRecord, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := make([]domain.ProjectRecord, 0, len(f.projects))
	for _, p := range f.projects {
		out = append(out, p)
	}
	return out, nil
}

// writeLegacyRoot writes the minimal legacy store the detection probe needs: a
// config.yaml with one project.
func writeLegacyRoot(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), ".agent-orchestrator")
	if err := os.MkdirAll(filepath.Join(root, "projects", "alpha", "sessions"), 0o750); err != nil {
		t.Fatal(err)
	}
	cfg := "projects:\n  alpha:\n    path: /repos/alpha\n    name: Alpha\n"
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestStatus_NoLegacyData(t *testing.T) {
	svc := New(Deps{Store: newFakeStore(), Root: filepath.Join(t.TempDir(), "nope")})
	st, err := svc.Status(context.Background())
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if st.Available {
		t.Fatal("Available = true with no legacy data, want false")
	}
}

func TestStatus_LegacyPresentEmptyDB(t *testing.T) {
	root := writeLegacyRoot(t)
	svc := New(Deps{Store: newFakeStore(), Root: root})
	st, err := svc.Status(context.Background())
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !st.Available {
		t.Fatal("Available = false with legacy data + empty DB, want true")
	}
	if st.LegacyRoot != root {
		t.Fatalf("LegacyRoot = %q, want %q", st.LegacyRoot, root)
	}
}

func TestStatus_AlreadyPopulated(t *testing.T) {
	root := writeLegacyRoot(t)
	store := newFakeStore()
	store.projects["existing"] = domain.ProjectRecord{ID: "existing"}
	svc := New(Deps{Store: store, Root: root})
	st, err := svc.Status(context.Background())
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if st.Available {
		t.Fatal("Available = true with a populated DB, want false")
	}
}

func TestStatus_ListError(t *testing.T) {
	root := writeLegacyRoot(t)
	store := newFakeStore()
	store.listErr = errors.New("boom")
	svc := New(Deps{Store: store, Root: root})
	if _, err := svc.Status(context.Background()); err == nil {
		t.Fatal("expected ListProjects error to propagate")
	}
}

func TestRun_ImportsThenStatusFlipsUnavailable(t *testing.T) {
	root := writeLegacyRoot(t)
	store := newFakeStore()
	svc := New(Deps{Store: store, Root: root, DataDir: filepath.Join(t.TempDir(), "data")})

	rep, err := svc.Run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if rep.ProjectsImported != 1 {
		t.Fatalf("projectsImported = %d, want 1", rep.ProjectsImported)
	}
	// After a successful import the DB is populated, so the offer retires.
	st, err := svc.Status(context.Background())
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if st.Available {
		t.Fatal("Available = true after import, want false")
	}
}

func TestNew_DefaultsRoot(t *testing.T) {
	svc := New(Deps{Store: newFakeStore()})
	if svc.root == "" {
		t.Fatal("empty Root should fall back to the default legacy root")
	}
}
