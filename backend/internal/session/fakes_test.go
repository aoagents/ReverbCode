package session

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// callLog records the cross-fake call order so tests can assert pipeline
// sequencing (e.g. OnKillRequested before Runtime.Destroy before Workspace.Destroy).
type callLog struct {
	mu    sync.Mutex
	calls []string
}

func (c *callLog) add(s string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, s)
}

func (c *callLog) snapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.calls))
	copy(out, c.calls)
	return out
}

// indexOf returns the position of the first call equal to name, or -1.
func (c *callLog) indexOf(name string) int {
	for i, s := range c.snapshot() {
		if s == name {
			return i
		}
	}
	return -1
}

// ---- fakeStore: in-memory LifecycleStore with full-row Upsert + Get ----

type fakeStore struct {
	mu       sync.Mutex
	records  map[domain.SessionID]*domain.SessionRecord
	metadata map[domain.SessionID]map[string]string
}

var _ ports.LifecycleStore = (*fakeStore)(nil)

func newFakeStore() *fakeStore {
	return &fakeStore{
		records:  map[domain.SessionID]*domain.SessionRecord{},
		metadata: map[domain.SessionID]map[string]string{},
	}
}

func (s *fakeStore) Upsert(_ context.Context, rec domain.SessionRecord, _ ports.EventType) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.records[rec.ID]; ok {
		if rec.Lifecycle.Revision != existing.Lifecycle.Revision {
			return fmt.Errorf("revision mismatch for %s: have %d, want %d", rec.ID, rec.Lifecycle.Revision, existing.Lifecycle.Revision)
		}
		rec.Lifecycle.Revision = existing.Lifecycle.Revision + 1
	} else {
		if rec.Lifecycle.Revision != 0 {
			return fmt.Errorf("revision mismatch for insert %s: have %d, want 0", rec.ID, rec.Lifecycle.Revision)
		}
		rec.Lifecycle.Revision = 1
	}
	if rec.Lifecycle.Version == 0 {
		rec.Lifecycle.Version = domain.LifecycleVersion
	}
	r := rec
	s.records[rec.ID] = &r
	return nil
}

func (s *fakeStore) Get(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[id]
	if !ok {
		return domain.SessionRecord{}, false, nil
	}
	return s.withMetadata(*rec), true, nil
}

func (s *fakeStore) Load(_ context.Context, id domain.SessionID) (domain.CanonicalSessionLifecycle, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[id]
	if !ok {
		return domain.CanonicalSessionLifecycle{}, false, nil
	}
	return rec.Lifecycle, true, nil
}

func (s *fakeStore) List(_ context.Context, project domain.ProjectID) ([]domain.SessionRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []domain.SessionRecord
	for _, rec := range s.records {
		if rec.ProjectID == project {
			out = append(out, s.withMetadata(*rec))
		}
	}
	return out, nil
}

func (s *fakeStore) GetMetadata(_ context.Context, id domain.SessionID) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneMap(s.metadata[id]), nil
}

func (s *fakeStore) PatchMetadata(_ context.Context, id domain.SessionID, kv map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.metadata[id] == nil {
		s.metadata[id] = map[string]string{}
	}
	for k, v := range kv {
		s.metadata[id][k] = v
	}
	return nil
}

// withMetadata attaches the separately-stored metadata to a record copy (a real
// store would return them together). Caller holds s.mu.
func (s *fakeStore) withMetadata(rec domain.SessionRecord) domain.SessionRecord {
	if md := s.metadata[rec.ID]; len(md) > 0 {
		rec.Metadata = cloneMap(md)
	}
	return rec
}

// ---- fakeRuntime ----

type fakeRuntime struct {
	log       *callLog
	createErr error
	alive     bool

	created   []ports.RuntimeConfig
	destroyed []ports.RuntimeHandle
	sent      []string
}

var _ ports.Runtime = (*fakeRuntime)(nil)

func (r *fakeRuntime) Create(_ context.Context, cfg ports.RuntimeConfig) (ports.RuntimeHandle, error) {
	r.log.add("Runtime.Create")
	if r.createErr != nil {
		return ports.RuntimeHandle{}, r.createErr
	}
	r.created = append(r.created, cfg)
	return ports.RuntimeHandle{ID: "rt-" + string(cfg.SessionID), RuntimeName: "tmux"}, nil
}

func (r *fakeRuntime) Destroy(_ context.Context, h ports.RuntimeHandle) error {
	r.log.add("Runtime.Destroy")
	r.destroyed = append(r.destroyed, h)
	return nil
}

func (r *fakeRuntime) SendMessage(_ context.Context, _ ports.RuntimeHandle, message string) error {
	r.sent = append(r.sent, message)
	return nil
}

func (r *fakeRuntime) GetOutput(_ context.Context, _ ports.RuntimeHandle, _ int) (string, error) {
	return "", nil
}

func (r *fakeRuntime) IsAlive(_ context.Context, _ ports.RuntimeHandle) (bool, error) {
	return r.alive, nil
}

// ---- fakeAgent ----

type fakeAgent struct {
	env map[string]string
}

var _ ports.Agent = (*fakeAgent)(nil)

func (a *fakeAgent) GetLaunchCommand(_ ports.AgentConfig) string { return "claude" }

func (a *fakeAgent) GetEnvironment(_ ports.AgentConfig) map[string]string { return cloneMap(a.env) }

func (a *fakeAgent) ProbeProcess(_ context.Context, _ ports.RuntimeHandle) (ports.ProcessProbe, error) {
	return ports.ProcessProbeAlive, nil
}

func (a *fakeAgent) GetRestoreCommand(agentSessionID string) string {
	return "claude --resume " + agentSessionID
}

// ---- fakeWorkspace (with worktree-remove refusal mode) ----

type fakeWorkspace struct {
	log        *callLog
	createErr  error
	refuse     map[string]bool // path -> still registered after prune (uncommitted work)
	created    []ports.WorkspaceConfig
	destroyed  []ports.WorkspaceInfo
	restoredID []domain.SessionID
}

var _ ports.Workspace = (*fakeWorkspace)(nil)

func (w *fakeWorkspace) Create(_ context.Context, cfg ports.WorkspaceConfig) (ports.WorkspaceInfo, error) {
	w.log.add("Workspace.Create")
	if w.createErr != nil {
		return ports.WorkspaceInfo{}, w.createErr
	}
	w.created = append(w.created, cfg)
	return workspaceFor(cfg), nil
}

func (w *fakeWorkspace) Destroy(_ context.Context, info ports.WorkspaceInfo) error {
	w.log.add("Workspace.Destroy")
	if w.refuse[info.Path] {
		// Worktree-remove safety: after `git worktree prune` the path is still
		// registered, so it may hold the agent's uncommitted work — refuse.
		return fmt.Errorf("workspace: refusing to rm -rf %s: still registered after prune", info.Path)
	}
	w.destroyed = append(w.destroyed, info)
	return nil
}

func (w *fakeWorkspace) List(_ context.Context, _ domain.ProjectID) ([]ports.WorkspaceInfo, error) {
	return nil, nil
}

func (w *fakeWorkspace) Restore(_ context.Context, cfg ports.WorkspaceConfig) (ports.WorkspaceInfo, error) {
	w.log.add("Workspace.Restore")
	w.restoredID = append(w.restoredID, cfg.SessionID)
	return workspaceFor(cfg), nil
}

func workspaceFor(cfg ports.WorkspaceConfig) ports.WorkspaceInfo {
	return ports.WorkspaceInfo{
		Path:      "/tmp/ws/" + string(cfg.SessionID),
		Branch:    cfg.Branch,
		SessionID: cfg.SessionID,
		ProjectID: cfg.ProjectID,
	}
}

// ---- recordingMessenger ----

type recordingMessenger struct {
	sent []struct {
		ID      domain.SessionID
		Message string
	}
}

var _ ports.AgentMessenger = (*recordingMessenger)(nil)

func (m *recordingMessenger) Send(_ context.Context, id domain.SessionID, message string) error {
	m.sent = append(m.sent, struct {
		ID      domain.SessionID
		Message string
	}{id, message})
	return nil
}

// ---- noopNotifier ----

type noopNotifier struct{}

var _ ports.Notifier = (*noopNotifier)(nil)

func (noopNotifier) Notify(_ context.Context, _ ports.OrchestratorEvent) error { return nil }

// ---- recordingLCM: wraps the REAL lifecycle.Manager and logs SM-facing calls ----

type recordingLCM struct {
	log   *callLog
	inner ports.LifecycleManager

	// onSpawnErr, when set, makes OnSpawnCompleted fail (without touching the
	// inner manager) so tests can exercise the SM's post-spawn failure paths.
	onSpawnErr error
}

var _ ports.LifecycleManager = (*recordingLCM)(nil)

func (l *recordingLCM) OnSpawnInitiated(ctx context.Context, rec domain.SessionRecord) error {
	l.log.add("OnSpawnInitiated")
	return l.inner.OnSpawnInitiated(ctx, rec)
}

func (l *recordingLCM) OnSpawnCompleted(ctx context.Context, id domain.SessionID, o ports.SpawnOutcome) error {
	l.log.add("OnSpawnCompleted")
	if l.onSpawnErr != nil {
		return l.onSpawnErr
	}
	return l.inner.OnSpawnCompleted(ctx, id, o)
}

func (l *recordingLCM) OnKillRequested(ctx context.Context, id domain.SessionID, r ports.KillReason) error {
	l.log.add("OnKillRequested")
	return l.inner.OnKillRequested(ctx, id, r)
}

func (l *recordingLCM) ApplySCMObservation(ctx context.Context, id domain.SessionID, f ports.SCMFacts) error {
	return l.inner.ApplySCMObservation(ctx, id, f)
}

func (l *recordingLCM) ApplyRuntimeObservation(ctx context.Context, id domain.SessionID, f ports.RuntimeFacts) error {
	return l.inner.ApplyRuntimeObservation(ctx, id, f)
}

func (l *recordingLCM) ApplyActivitySignal(ctx context.Context, id domain.SessionID, s ports.ActivitySignal) error {
	return l.inner.ApplyActivitySignal(ctx, id, s)
}

func (l *recordingLCM) TickEscalations(ctx context.Context, now time.Time) error {
	return l.inner.TickEscalations(ctx, now)
}

func (l *recordingLCM) RunningSessions(ctx context.Context) ([]domain.SessionRecord, error) {
	return l.inner.RunningSessions(ctx)
}

// ---- harness: wires the SM against the fakes + the real LCM ----

type harness struct {
	sm        *Manager
	store     *fakeStore
	runtime   *fakeRuntime
	agent     *fakeAgent
	workspace *fakeWorkspace
	messenger *recordingMessenger
	lcm       *recordingLCM
	log       *callLog
}

var fixedTime = time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)

func newHarness(id domain.SessionID) *harness {
	log := &callLog{}
	store := newFakeStore()
	rt := &fakeRuntime{log: log, alive: true}
	ag := &fakeAgent{env: map[string]string{"BASE": "1"}}
	ws := &fakeWorkspace{log: log, refuse: map[string]bool{}}
	msg := &recordingMessenger{}

	lcm := &recordingLCM{log: log, inner: lifecycle.New(store, noopNotifier{}, msg)}

	sm := New(Deps{
		Runtime:   rt,
		Agent:     ag,
		Workspace: ws,
		Store:     store,
		Messenger: msg,
		Lifecycle: lcm,
		Clock:     func() time.Time { return fixedTime },
		NewID:     func(ports.SpawnConfig) domain.SessionID { return id },
	})

	return &harness{sm: sm, store: store, runtime: rt, agent: ag, workspace: ws, messenger: msg, lcm: lcm, log: log}
}

func cloneMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
