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

// ---- fakes ----

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
func (f *fakeStore) ListAllSessions(_ context.Context) ([]domain.SessionRecord, error) {
	out := make([]domain.SessionRecord, 0, len(f.sessions))
	for _, r := range f.sessions {
		out = append(out, r)
	}
	return out, nil
}
func (f *fakeStore) PRFactsForSession(_ context.Context, id domain.SessionID) (domain.PRFacts, error) {
	return f.pr[id], nil
}

// fakeLCM is the minimal lifecycle the Session Manager drives: it persists the
// spawn/kill canonical writes into the store so Get reflects them.
type fakeLCM struct {
	store     *fakeStore
	completed int
}

func (l *fakeLCM) OnSpawnCompleted(_ context.Context, id domain.SessionID, o ports.SpawnOutcome) error {
	l.completed++
	rec := l.store.sessions[id]
	rec.Lifecycle.Session.State = domain.SessionNotStarted
	rec.Lifecycle.IsAlive = true
	rec.Lifecycle.TerminationReason = domain.TermNone
	rec.Metadata = domain.SessionMetadata{
		Branch: o.Branch, WorkspacePath: o.WorkspacePath,
		RuntimeHandleID: o.RuntimeHandle.ID, RuntimeName: o.RuntimeHandle.RuntimeName,
		AgentSessionID: o.AgentSessionID, Prompt: o.Prompt,
	}
	l.store.sessions[id] = rec
	return nil
}
func (l *fakeLCM) OnKillRequested(_ context.Context, id domain.SessionID, reason domain.TerminationReason) error {
	rec := l.store.sessions[id]
	rec.Lifecycle.Session.State = domain.SessionTerminated
	rec.Lifecycle.TerminationReason = reason
	rec.Lifecycle.IsAlive = false
	l.store.sessions[id] = rec
	return nil
}
func (l *fakeLCM) ApplyRuntimeObservation(context.Context, domain.SessionID, ports.RuntimeFacts) error {
	return nil
}
func (l *fakeLCM) ApplyActivitySignal(context.Context, domain.SessionID, ports.ActivitySignal) error {
	return nil
}
func (l *fakeLCM) ApplyPRObservation(context.Context, domain.SessionID, ports.PRObservation) error {
	return nil
}
func (l *fakeLCM) TickEscalations(context.Context, time.Time) error { return nil }
func (l *fakeLCM) RunningSessions(context.Context) ([]domain.SessionRecord, error) {
	return nil, nil
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
	return ports.RuntimeHandle{ID: "h1", RuntimeName: "tmux"}, nil
}
func (r *fakeRuntime) Destroy(context.Context, ports.RuntimeHandle) error { r.destroyed++; return nil }
func (r *fakeRuntime) IsAlive(context.Context, ports.RuntimeHandle) (bool, error) {
	return true, nil
}

type fakeAgent struct{}

func (fakeAgent) GetConfigSpec(context.Context) (ports.ConfigSpec, error) {
	return ports.ConfigSpec{}, nil
}
func (fakeAgent) GetLaunchCommand(context.Context, ports.LaunchConfig) ([]string, error) {
	return []string{"launch"}, nil
}
func (fakeAgent) GetPromptDeliveryStrategy(context.Context, ports.LaunchConfig) (ports.PromptDeliveryStrategy, error) {
	return ports.PromptDeliveryInCommand, nil
}
func (fakeAgent) GetAgentHooks(context.Context, ports.WorkspaceHookConfig) error { return nil }
func (fakeAgent) GetRestoreCommand(_ context.Context, cfg ports.RestoreConfig) ([]string, bool, error) {
	if id := cfg.Session.Metadata[ports.MetadataKeyAgentSessionID]; id != "" {
		return []string{"resume", id}, true, nil
	}
	return nil, false, nil
}
func (fakeAgent) SessionInfo(context.Context, ports.SessionRef) (ports.SessionInfo, bool, error) {
	return ports.SessionInfo{}, false, nil
}

// fakeAgents resolves every harness (including the empty one the spawn/restore
// tests use) to the same fakeAgent, exercising the resolver path without caring
// which harness is requested.
type fakeAgents struct{}

func (fakeAgents) Agent(domain.AgentHarness) (ports.Agent, bool) { return fakeAgent{}, true }

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
	m := New(Deps{
		Runtime: rt, Agents: fakeAgents{}, Workspace: ws,
		Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
	})
	return m, st, rt, ws
}

func seedTerminal(st *fakeStore, id domain.SessionID, meta domain.SessionMetadata) {
	st.sessions[id] = domain.SessionRecord{
		ID: id, ProjectID: "mer", Metadata: meta,
		Lifecycle: domain.CanonicalSessionLifecycle{Session: domain.SessionSubstate{State: domain.SessionTerminated}},
	}
}

// ---- tests ----

func TestSpawn_AssignsIDAndGoesLive(t *testing.T) {
	m, st, rt, _ := newManager()

	s, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Prompt: "do it"})
	if err != nil {
		t.Fatal(err)
	}
	if s.ID != "mer-1" {
		t.Fatalf("store should assign mer-1, got %q", s.ID)
	}
	if s.Status != domain.StatusSpawning {
		t.Fatalf("fresh session displays spawning, got %q", s.Status)
	}
	if rt.created != 1 {
		t.Fatalf("runtime not created")
	}
	if st.sessions["mer-1"].Metadata.RuntimeHandleID != "h1" {
		t.Fatal("spawn handle not folded into the row")
	}
}

func TestSpawn_RollsBackOnRuntimeFailure(t *testing.T) {
	m, st, _, ws := newManager()
	m.runtime = &fakeRuntime{createErr: errors.New("boom")}

	if _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer"}); err == nil {
		t.Fatal("expected spawn to fail")
	}
	if ws.destroyed != 1 {
		t.Fatal("workspace should be rolled back")
	}
	if st.sessions["mer-1"].Lifecycle.Session.State != domain.SessionTerminated {
		t.Fatal("orphaned spawn should be parked terminal")
	}
}

func TestKill_TearsDownRuntimeAndWorkspace(t *testing.T) {
	m, st, rt, ws := newManager()
	st.sessions["mer-1"] = mkLive("mer-1")

	freed, err := m.Kill(ctx, "mer-1", domain.TermManuallyKilled)
	if err != nil || !freed {
		t.Fatalf("kill should free the workspace: freed=%v err=%v", freed, err)
	}
	if rt.destroyed != 1 || ws.destroyed != 1 {
		t.Fatal("kill should destroy runtime and workspace")
	}
}

func TestKill_RefusesIncompleteHandle(t *testing.T) {
	m, st, _, _ := newManager()
	st.sessions["mer-1"] = domain.SessionRecord{ // live, but no teardown handles
		ID: "mer-1", ProjectID: "mer",
		Lifecycle: domain.CanonicalSessionLifecycle{Session: domain.SessionSubstate{State: domain.SessionWorking}, IsAlive: true},
	}

	if _, err := m.Kill(ctx, "mer-1", domain.TermManuallyKilled); !errors.Is(err, ErrIncompleteHandle) {
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
	if s.Status != domain.StatusSpawning {
		t.Fatalf("restored session displays spawning, got %q", s.Status)
	}
	if rt.created != 1 {
		t.Fatal("restore should relaunch the runtime")
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
	st.pr["mer-1"] = domain.PRFacts{Exists: true, CI: domain.CIFailing}

	list, err := m.List(ctx, "mer")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Status != domain.StatusCIFailed {
		t.Fatalf("status should reflect PR facts, got %+v", list)
	}
}

func TestCleanup_ReclaimsTerminalWorkspaces(t *testing.T) {
	m, st, _, ws := newManager()
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1"})
	st.sessions["mer-2"] = mkLive("mer-2") // live: must be skipped

	cleaned, err := m.Cleanup(ctx, "mer")
	if err != nil {
		t.Fatal(err)
	}
	if len(cleaned) != 1 || cleaned[0] != "mer-1" {
		t.Fatalf("only the terminal session should be reclaimed, got %v", cleaned)
	}
	if ws.destroyed != 1 {
		t.Fatal("the live session's workspace must not be destroyed")
	}
}

func mkLive(id domain.SessionID) domain.SessionRecord {
	return domain.SessionRecord{
		ID: id, ProjectID: "mer",
		Metadata:  domain.SessionMetadata{WorkspacePath: "/ws/" + string(id), RuntimeHandleID: "h1", RuntimeName: "tmux"},
		Lifecycle: domain.CanonicalSessionLifecycle{Session: domain.SessionSubstate{State: domain.SessionWorking}, IsAlive: true},
	}
}
