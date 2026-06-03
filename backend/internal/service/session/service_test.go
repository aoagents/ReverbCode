package session

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type fakeStore struct {
	sessions map[domain.SessionID]domain.SessionRecord
	pr       map[domain.SessionID]domain.PRFacts
	num      int
}

func newFakeStore() *fakeStore {
	return &fakeStore{sessions: map[domain.SessionID]domain.SessionRecord{}, pr: map[domain.SessionID]domain.PRFacts{}}
}

func (f *fakeStore) CreateSession(_ context.Context, rec domain.SessionRecord) (domain.SessionRecord, error) {
	f.num++
	rec.ID = domain.SessionID(fmt.Sprintf("%s-%d", rec.ProjectID, f.num))
	f.sessions[rec.ID] = rec
	return rec, nil
}

func (f *fakeStore) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	r, ok := f.sessions[id]
	return r, ok, nil
}

func (f *fakeStore) ListSessions(_ context.Context, p domain.ProjectID) ([]domain.SessionRecord, error) {
	var out []domain.SessionRecord
	for _, r := range f.sessions {
		if r.ProjectID == p {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeStore) ListAllSessions(_ context.Context) ([]domain.SessionRecord, error) {
	out := make([]domain.SessionRecord, 0, len(f.sessions))
	for _, r := range f.sessions {
		out = append(out, r)
	}
	return out, nil
}

func (f *fakeStore) RenameSession(_ context.Context, id domain.SessionID, displayName string, updatedAt time.Time) (bool, error) {
	r, ok := f.sessions[id]
	if !ok {
		return false, nil
	}
	r.DisplayName = displayName
	r.UpdatedAt = updatedAt
	f.sessions[id] = r
	return true, nil
}

func (f *fakeStore) GetDisplayPRFactsForSession(_ context.Context, id domain.SessionID) (domain.PRFacts, bool, error) {
	pr, ok := f.pr[id]
	return pr, ok, nil
}

func TestSessionListDerivesStatusFromPRFacts(t *testing.T) {
	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Activity: domain.Activity{State: domain.ActivityActive}}
	st.pr["mer-1"] = domain.PRFacts{URL: "pr1", CI: domain.CIFailing}

	list, err := (&Service{store: st}).List(context.Background(), ListFilter{ProjectID: "mer"})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Status != domain.StatusCIFailed {
		t.Fatalf("got %+v", list)
	}
}

func TestSessionRenameUpdatesDisplayName(t *testing.T) {
	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer"}

	err := (&Service{store: st}).Rename(context.Background(), "mer-1", "  Fix issue #90  ")
	if err != nil {
		t.Fatal(err)
	}
	if got := st.sessions["mer-1"].DisplayName; got != "Fix issue #90" {
		t.Fatalf("display name = %q, want trimmed rename", got)
	}
}

func TestSessionRenameMissingSessionReturnsNotFound(t *testing.T) {
	st := newFakeStore()

	err := (&Service{store: st}).Rename(context.Background(), "mer-404", "Missing")
	var e *apierr.Error
	if !errors.As(err, &e) || e.Kind != apierr.KindNotFound || e.Code != "SESSION_NOT_FOUND" {
		t.Fatalf("err = %v, want apierr NotFound SESSION_NOT_FOUND", err)
	}
}

// fakeCommander records Kill/Spawn calls so a test can assert the
// clean-orchestrator ordering without wiring a real session engine.
type fakeCommander struct {
	killed       []domain.SessionID
	spawned      bool
	killsAtSpawn int
}

func (f *fakeCommander) Spawn(_ context.Context, cfg ports.SpawnConfig) (domain.SessionRecord, error) {
	f.spawned = true
	f.killsAtSpawn = len(f.killed)
	return domain.SessionRecord{ID: "mer-9", ProjectID: cfg.ProjectID, Kind: cfg.Kind}, nil
}
func (f *fakeCommander) Restore(context.Context, domain.SessionID) (domain.SessionRecord, error) {
	return domain.SessionRecord{}, nil
}
func (f *fakeCommander) Kill(_ context.Context, id domain.SessionID) (bool, error) {
	f.killed = append(f.killed, id)
	return true, nil
}
func (f *fakeCommander) Send(context.Context, domain.SessionID, string) error { return nil }
func (f *fakeCommander) Cleanup(context.Context, domain.ProjectID) ([]domain.SessionID, error) {
	return nil, nil
}

func TestSpawnOrchestratorCleanKillsActiveOrchestratorsBeforeSpawn(t *testing.T) {
	st := newFakeStore()
	// Two active orchestrators plus an unrelated worker and a terminated
	// orchestrator that must be left alone.
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindOrchestrator}
	st.sessions["mer-2"] = domain.SessionRecord{ID: "mer-2", ProjectID: "mer", Kind: domain.KindOrchestrator}
	st.sessions["mer-3"] = domain.SessionRecord{ID: "mer-3", ProjectID: "mer", Kind: domain.KindWorker}
	st.sessions["mer-4"] = domain.SessionRecord{ID: "mer-4", ProjectID: "mer", Kind: domain.KindOrchestrator, IsTerminated: true}

	fc := &fakeCommander{}
	svc := &Service{manager: fc, store: st}

	if _, err := svc.SpawnOrchestrator(context.Background(), "mer", true); err != nil {
		t.Fatalf("SpawnOrchestrator: %v", err)
	}

	if len(fc.killed) != 2 {
		t.Fatalf("killed = %v, want the two active orchestrators", fc.killed)
	}
	if !fc.spawned || fc.killsAtSpawn != 2 {
		t.Fatalf("spawn must run after both kills: spawned=%v killsAtSpawn=%d", fc.spawned, fc.killsAtSpawn)
	}
}

func TestSpawnOrchestratorNoCleanSkipsKills(t *testing.T) {
	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindOrchestrator}

	fc := &fakeCommander{}
	svc := &Service{manager: fc, store: st}

	if _, err := svc.SpawnOrchestrator(context.Background(), "mer", false); err != nil {
		t.Fatalf("SpawnOrchestrator: %v", err)
	}
	if len(fc.killed) != 0 || !fc.spawned {
		t.Fatalf("clean=false must spawn without kills: killed=%v spawned=%v", fc.killed, fc.spawned)
	}
}
