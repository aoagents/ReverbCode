package integration

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/cdc"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	prsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/pr"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

type stubRuntime struct{ created, destroyed int }

func (s *stubRuntime) Create(context.Context, ports.RuntimeConfig) (ports.RuntimeHandle, error) {
	s.created++
	return ports.RuntimeHandle{ID: "h1"}, nil
}
func (s *stubRuntime) Destroy(context.Context, ports.RuntimeHandle) error         { s.destroyed++; return nil }
func (s *stubRuntime) IsAlive(context.Context, ports.RuntimeHandle) (bool, error) { return true, nil }

type stubAgent struct{}

func (stubAgent) GetConfigSpec(context.Context) (ports.ConfigSpec, error) {
	return ports.ConfigSpec{}, nil
}
func (stubAgent) GetLaunchCommand(context.Context, ports.LaunchConfig) ([]string, error) {
	return []string{"launch"}, nil
}
func (stubAgent) GetPromptDeliveryStrategy(context.Context, ports.LaunchConfig) (ports.PromptDeliveryStrategy, error) {
	return ports.PromptDeliveryInCommand, nil
}
func (stubAgent) GetAgentHooks(context.Context, ports.WorkspaceHookConfig) error { return nil }
func (stubAgent) GetRestoreCommand(_ context.Context, cfg ports.RestoreConfig) ([]string, bool, error) {
	if id := cfg.Session.Metadata[ports.MetadataKeyAgentSessionID]; id != "" {
		return []string{"resume", id}, true, nil
	}
	return nil, false, nil
}
func (stubAgent) SessionInfo(context.Context, ports.SessionRef) (ports.SessionInfo, bool, error) {
	return ports.SessionInfo{}, false, nil
}

// stubAgents resolves every harness to the same stubAgent.
type stubAgents struct{}

func (stubAgents) Agent(domain.AgentHarness) (ports.Agent, bool) { return stubAgent{}, true }

type stubWorkspace struct{ destroyed int }

func (s *stubWorkspace) Create(_ context.Context, cfg ports.WorkspaceConfig) (ports.WorkspaceInfo, error) {
	return ports.WorkspaceInfo{Path: "/ws/" + string(cfg.SessionID), Branch: cfg.Branch, SessionID: cfg.SessionID, ProjectID: cfg.ProjectID}, nil
}
func (s *stubWorkspace) Destroy(context.Context, ports.WorkspaceInfo) error {
	s.destroyed++
	return nil
}
func (s *stubWorkspace) Restore(ctx context.Context, cfg ports.WorkspaceConfig) (ports.WorkspaceInfo, error) {
	return s.Create(ctx, cfg)
}

type captureMessenger struct{ msgs []string }

func (c *captureMessenger) Send(_ context.Context, _ domain.SessionID, msg string) error {
	c.msgs = append(c.msgs, msg)
	return nil
}

type stack struct {
	store *sqlite.Store
	sm    *sessionsvc.Service
	lcm   *lifecycle.Manager
	prm   *prsvc.Manager
	rt    *stubRuntime
	ws    *stubWorkspace
	msg   *captureMessenger
}

func newStack(t *testing.T) *stack {
	t.Helper()
	ctx := context.Background()
	store, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.UpsertProject(ctx, domain.ProjectRecord{
		ID:           "mer",
		Path:         "/repo/mer",
		RegisteredAt: time.Now(),
		Config: domain.ProjectConfig{
			Worker:       domain.RoleOverride{Harness: domain.HarnessClaudeCode},
			Orchestrator: domain.RoleOverride{Harness: domain.HarnessClaudeCode},
		},
	}); err != nil {
		t.Fatal(err)
	}
	msg := &captureMessenger{}
	lcm := lifecycle.New(store, msg)
	prm := prsvc.New(prsvc.Deps{Writer: store, Lifecycle: lcm})
	rt := &stubRuntime{}
	ws := &stubWorkspace{}
	mgr := sessionmanager.New(sessionmanager.Deps{Runtime: rt, Agents: stubAgents{}, Workspace: ws, Store: store, Messenger: msg, Lifecycle: lcm, LookPath: func(string) (string, error) { return "/usr/bin/true", nil }})
	sm := sessionsvc.New(mgr, store)
	return &stack{store: store, sm: sm, lcm: lcm, prm: prm, rt: rt, ws: ws, msg: msg}
}

func TestSpawnPRKillRoundTrip(t *testing.T) {
	ctx := context.Background()
	st := newStack(t)
	sess, err := st.sm.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Branch: "b", Prompt: "do it"})
	if err != nil {
		t.Fatal(err)
	}
	if sess.ID != "mer-1" || sess.Status != domain.StatusIdle {
		t.Fatalf("spawn got %+v", sess)
	}
	rec, ok, _ := st.store.GetSession(ctx, sess.ID)
	if !ok || rec.Metadata.RuntimeHandleID != "h1" || rec.IsTerminated {
		t.Fatalf("post-spawn row wrong: %+v", rec)
	}
	if err := st.prm.ApplyObservation(ctx, sess.ID, ports.PRObservation{Fetched: true, URL: "pr1", Number: 1, CI: domain.CIFailing, Checks: []ports.PRCheckObservation{{Name: "build", CommitHash: "c1", Status: domain.PRCheckFailed, LogTail: "boom"}}}); err != nil {
		t.Fatal(err)
	}
	got, err := st.sm.Get(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.StatusCIFailed {
		t.Fatalf("want ci_failed, got %q", got.Status)
	}
	freed, err := st.sm.Kill(ctx, sess.ID)
	if err != nil || !freed {
		t.Fatalf("kill freed=%v err=%v", freed, err)
	}
	rec, _, _ = st.store.GetSession(ctx, sess.ID)
	if !rec.IsTerminated {
		t.Fatalf("post-kill row should be terminated: %+v", rec)
	}
}

func TestRestoreRoundTripPreservesMetadata(t *testing.T) {
	ctx := context.Background()
	st := newStack(t)
	sess, err := st.sm.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Branch: "b", Prompt: "prompt"})
	if err != nil {
		t.Fatal(err)
	}
	rec, _, _ := st.store.GetSession(ctx, sess.ID)
	rec.Metadata.AgentSessionID = "agent-x"
	if err := st.store.UpdateSession(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if _, err := st.sm.Kill(ctx, sess.ID); err != nil {
		t.Fatal(err)
	}
	restored, err := st.sm.Restore(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.IsTerminated || restored.Metadata.AgentSessionID != "agent-x" {
		t.Fatalf("restored wrong: %+v", restored)
	}
}

func TestCDCPollerReceivesSessionAndPREvents(t *testing.T) {
	ctx := context.Background()
	st := newStack(t)
	b := cdc.NewBroadcaster()
	var got []cdc.Event
	b.Subscribe(func(e cdc.Event) { got = append(got, e) })
	poller := cdc.NewPoller(st.store, b, cdc.PollerConfig{})
	sess, err := st.sm.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.prm.ApplyObservation(ctx, sess.ID, ports.PRObservation{Fetched: true, URL: "pr1", Number: 1, Review: domain.ReviewApproved}); err != nil {
		t.Fatal(err)
	}
	if err := poller.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	if len(got) < 2 {
		t.Fatalf("want CDC events, got %d", len(got))
	}
}
