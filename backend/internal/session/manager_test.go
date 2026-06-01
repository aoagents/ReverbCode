package session

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var ctx = context.Background()

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
func (f *fakeStore) UpdateSession(_ context.Context, rec domain.SessionRecord) error {
	f.sessions[rec.ID] = rec
	return nil
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
func (f *fakeStore) ListAllSessions(context.Context) ([]domain.SessionRecord, error) {
	var out []domain.SessionRecord
	for _, r := range f.sessions {
		out = append(out, r)
	}
	return out, nil
}
func (f *fakeStore) GetDisplayPRFactsForSession(_ context.Context, id domain.SessionID) (domain.PRFacts, bool, error) {
	if pr := f.pr[id]; pr.URL != "" {
		return pr, true, nil
	}
	return domain.PRFacts{}, false, nil
}

type fakeLCM struct {
	store     *fakeStore
	completed int
}

func (l *fakeLCM) MarkSpawned(_ context.Context, id domain.SessionID, metadata domain.SessionMetadata) error {
	l.completed++
	rec := l.store.sessions[id]
	rec.IsTerminated = false
	rec.Activity = domain.ActivitySubstate{State: domain.ActivityIdle, LastActivityAt: time.Now(), Source: domain.SourceRuntime}
	rec.Metadata = metadata
	l.store.sessions[id] = rec
	return nil
}
func (l *fakeLCM) MarkTerminated(_ context.Context, id domain.SessionID) error {
	rec := l.store.sessions[id]
	rec.IsTerminated = true
	rec.Activity = domain.ActivitySubstate{State: domain.ActivityExited, LastActivityAt: time.Now(), Source: domain.SourceRuntime}
	l.store.sessions[id] = rec
	return nil
}

type fakeRuntime struct {
	createErr          error
	created, destroyed int
}

func (r *fakeRuntime) Create(context.Context, ports.RuntimeConfig) (ports.RuntimeHandle, error) {
	if r.createErr != nil {
		return ports.RuntimeHandle{}, r.createErr
	}
	r.created++
	return ports.RuntimeHandle{ID: "h1"}, nil
}
func (r *fakeRuntime) Destroy(context.Context, ports.RuntimeHandle) error { r.destroyed++; return nil }

type fakeAgent struct{}

func (fakeAgent) GetLaunchCommand(ports.AgentConfig) string { return "launch" }
func (fakeAgent) GetEnvironment(ports.AgentConfig) map[string]string {
	return map[string]string{"X": "1"}
}
func (fakeAgent) GetRestoreCommand(id string) string { return "resume " + id }

type fakeWorkspace struct {
	destroyErr error
	destroyed  int
}

func (w *fakeWorkspace) Create(_ context.Context, cfg ports.WorkspaceConfig) (ports.WorkspaceInfo, error) {
	return ports.WorkspaceInfo{Path: "/ws/" + string(cfg.SessionID), Branch: cfg.Branch, SessionID: cfg.SessionID, ProjectID: cfg.ProjectID}, nil
}
func (w *fakeWorkspace) Destroy(context.Context, ports.WorkspaceInfo) error {
	w.destroyed++
	return w.destroyErr
}
func (w *fakeWorkspace) Restore(ctx context.Context, cfg ports.WorkspaceConfig) (ports.WorkspaceInfo, error) {
	return w.Create(ctx, cfg)
}

type fakeMessenger struct{ msgs []string }

func (m *fakeMessenger) Send(_ context.Context, _ domain.SessionID, msg string) error {
	m.msgs = append(m.msgs, msg)
	return nil
}

func newManager() (*Manager, *fakeStore, *fakeRuntime, *fakeWorkspace) {
	st := newFakeStore()
	rt := &fakeRuntime{}
	ws := &fakeWorkspace{}
	m := New(Deps{Runtime: rt, Agent: fakeAgent{}, Workspace: ws, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}})
	return m, st, rt, ws
}
func seedTerminal(st *fakeStore, id domain.SessionID, meta domain.SessionMetadata) {
	st.sessions[id] = domain.SessionRecord{ID: id, ProjectID: "mer", Metadata: meta, IsTerminated: true, Activity: domain.ActivitySubstate{State: domain.ActivityExited}}
}
func mkLive(id domain.SessionID) domain.SessionRecord {
	return domain.SessionRecord{ID: id, ProjectID: "mer", Metadata: domain.SessionMetadata{WorkspacePath: "/ws/" + string(id), RuntimeHandleID: "h1"}, Activity: domain.ActivitySubstate{State: domain.ActivityActive}}
}

func TestSpawn_AssignsIDAndGoesIdle(t *testing.T) {
	m, st, rt, _ := newManager()
	s, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Prompt: "do it"})
	if err != nil {
		t.Fatal(err)
	}
	if s.ID != "mer-1" {
		t.Fatalf("got %q", s.ID)
	}
	if s.Status != domain.StatusIdle {
		t.Fatalf("fresh session displays idle, got %q", s.Status)
	}
	if rt.created != 1 {
		t.Fatal("runtime not created")
	}
	if st.sessions["mer-1"].Metadata.RuntimeHandleID != "h1" {
		t.Fatal("handle not folded")
	}
}
func TestSpawn_RollsBackOnRuntimeFailure(t *testing.T) {
	m, st, _, ws := newManager()
	m.runtime = &fakeRuntime{createErr: errors.New("boom")}
	if _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer"}); err == nil {
		t.Fatal("expected failure")
	}
	if ws.destroyed != 1 {
		t.Fatal("workspace should roll back")
	}
	if !st.sessions["mer-1"].IsTerminated {
		t.Fatal("orphaned spawn should be terminated")
	}
}
func TestKill_TearsDownRuntimeAndWorkspace(t *testing.T) {
	m, st, rt, ws := newManager()
	st.sessions["mer-1"] = mkLive("mer-1")
	freed, err := m.Kill(ctx, "mer-1")
	if err != nil || !freed {
		t.Fatalf("freed=%v err=%v", freed, err)
	}
	if rt.destroyed != 1 || ws.destroyed != 1 {
		t.Fatal("kill should destroy runtime and workspace")
	}
}
func TestKill_RefusesIncompleteHandle(t *testing.T) {
	m, st, _, _ := newManager()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Activity: domain.ActivitySubstate{State: domain.ActivityActive}}
	if _, err := m.Kill(ctx, "mer-1"); !errors.Is(err, ErrIncompleteHandle) {
		t.Fatalf("want ErrIncompleteHandle, got %v", err)
	}
}
func TestRestore_ReopensTerminal(t *testing.T) {
	m, st, rt, _ := newManager()
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "b", AgentSessionID: "agent-x"})
	s, err := m.Restore(ctx, "mer-1")
	if err != nil {
		t.Fatal(err)
	}
	if s.Status != domain.StatusIdle {
		t.Fatalf("restored displays idle, got %q", s.Status)
	}
	if rt.created != 1 {
		t.Fatal("restore should relaunch")
	}
}
func TestRestore_RefusesLiveSession(t *testing.T) {
	m, st, _, _ := newManager()
	st.sessions["mer-1"] = mkLive("mer-1")
	if _, err := m.Restore(ctx, "mer-1"); !errors.Is(err, ErrNotRestorable) {
		t.Fatalf("want ErrNotRestorable, got %v", err)
	}
}
func TestList_DerivesStatusFromPRFacts(t *testing.T) {
	m, st, _, _ := newManager()
	st.sessions["mer-1"] = mkLive("mer-1")
	st.pr["mer-1"] = domain.PRFacts{URL: "pr1", CI: domain.CIFailing}
	list, err := m.List(ctx, "mer")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Status != domain.StatusCIFailed {
		t.Fatalf("got %+v", list)
	}
}
func TestCleanup_ReclaimsTerminalWorkspaces(t *testing.T) {
	m, st, _, ws := newManager()
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1"})
	st.sessions["mer-2"] = mkLive("mer-2")
	cleaned, err := m.Cleanup(ctx, "mer")
	if err != nil {
		t.Fatal(err)
	}
	if len(cleaned) != 1 || cleaned[0] != "mer-1" {
		t.Fatalf("got %v", cleaned)
	}
	if ws.destroyed != 1 {
		t.Fatal("live workspace must not be destroyed")
	}
}
