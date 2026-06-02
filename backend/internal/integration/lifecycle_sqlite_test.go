// Package integration exercises the lifecycle + session lane against the real
// SQLite store and the real CDC trigger pipeline. Unit tests stay on the
// in-memory fakes in lifecycle/ and session/; these live-fire tests prove the
// wiring across packages actually flows: SM -> store row -> LCM mutate -> store
// update -> DB trigger -> change_log read.
package integration

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/cdc"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/notification"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/session"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// ---- plugin fakes (minimal: only enough to drive SM through real LCM) ----

type stubRuntime struct {
	id, name string
}

func (s *stubRuntime) Create(_ context.Context, cfg ports.RuntimeConfig) (ports.RuntimeHandle, error) {
	return ports.RuntimeHandle{ID: s.id, RuntimeName: s.name}, nil
}
func (s *stubRuntime) Destroy(context.Context, ports.RuntimeHandle) error { return nil }
func (s *stubRuntime) IsAlive(context.Context, ports.RuntimeHandle) (bool, error) {
	return true, nil
}

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

type stubWorkspace struct {
	root string
}

func (w *stubWorkspace) Create(_ context.Context, cfg ports.WorkspaceConfig) (ports.WorkspaceInfo, error) {
	return ports.WorkspaceInfo{
		Path:      filepath.Join(w.root, string(cfg.SessionID)),
		Branch:    cfg.Branch,
		SessionID: cfg.SessionID,
		ProjectID: cfg.ProjectID,
	}, nil
}
func (w *stubWorkspace) Destroy(context.Context, ports.WorkspaceInfo) error { return nil }
func (w *stubWorkspace) Restore(ctx context.Context, cfg ports.WorkspaceConfig) (ports.WorkspaceInfo, error) {
	return w.Create(ctx, cfg)
}

type captureMessenger struct {
	mu   sync.Mutex
	msgs []string
}

func (m *captureMessenger) Send(_ context.Context, _ domain.SessionID, msg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.msgs = append(m.msgs, msg)
	return nil
}
func (m *captureMessenger) drain() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := append([]string(nil), m.msgs...)
	m.msgs = nil
	return out
}

type captureNotifier struct {
	mu     sync.Mutex
	events []ports.Event
}

func (n *captureNotifier) Notify(_ context.Context, e ports.Event) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.events = append(n.events, e)
	return nil
}
func (n *captureNotifier) drain() []ports.Event {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := append([]ports.Event(nil), n.events...)
	n.events = nil
	return out
}

// ---- harness: real store + real LCM + real SM + change_log poller ----

type liveStack struct {
	dataDir   string
	store     *sqlite.Store
	lcm       *lifecycle.Manager
	sm        *session.Manager
	notifier  *captureNotifier
	messenger *captureMessenger

	closed bool // guard so the explicit close() and t.Cleanup don't double-close
}

// openLiveStack opens the store + hydrates the LCM/SM and registers an
// idempotent t.Cleanup so a mid-test t.Fatalf can't leak the SQLite handle.
// Tests that need to simulate a daemon restart still call close() explicitly
// between phases; the cleanup hook becomes a no-op once that runs.
func openLiveStack(t *testing.T, dataDir string) *liveStack {
	t.Helper()
	store, err := sqlite.Open(dataDir)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	notifier := &captureNotifier{}
	messenger := &captureMessenger{}
	lcm := lifecycle.New(store, store, notifier, messenger)

	wsRoot := t.TempDir()
	sm := session.New(session.Deps{
		Runtime:   &stubRuntime{id: "h1", name: "tmux"},
		Agents:    stubAgents{},
		Workspace: &stubWorkspace{root: wsRoot},
		Store:     store,
		Messenger: messenger,
		Lifecycle: lcm,
	})
	st := &liveStack{
		dataDir:   dataDir,
		store:     store,
		lcm:       lcm,
		sm:        sm,
		notifier:  notifier,
		messenger: messenger,
	}
	t.Cleanup(func() {
		if st.closed {
			return
		}
		// Best-effort: failures here would be noise after t.Fatalf already
		// recorded the real cause.
		_ = st.store.Close()
		st.closed = true
	})
	return st
}

func (s *liveStack) close(t *testing.T) {
	t.Helper()
	if s.closed {
		return
	}
	s.closed = true
	if err := s.store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
}

func seedProject(t *testing.T, store *sqlite.Store, id string) {
	t.Helper()
	if err := store.UpsertProject(context.Background(), sqlite.ProjectRow{
		ID: id, Path: "/repo/" + id, RegisteredAt: time.Now(),
	}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}
}

func durableLifecycle(store *sqlite.Store, messenger ports.AgentMessenger) *lifecycle.Manager {
	renderer := notification.NewRenderer(store)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	notifier := notification.NewEnqueuer(store, renderer, logger)
	return lifecycle.New(store, store, notifier, messenger)
}

func durableRecord(project, issue, branch string) domain.SessionRecord {
	now := time.Now().UTC().Truncate(time.Second)
	return domain.SessionRecord{
		ProjectID: domain.ProjectID(project),
		IssueID:   domain.IssueID(issue),
		Kind:      domain.KindWorker,
		Lifecycle: domain.CanonicalSessionLifecycle{
			Version: domain.LifecycleVersion,
			Session: domain.SessionSubstate{State: domain.SessionWorking},
			IsAlive: true,
			Activity: domain.ActivitySubstate{
				State: domain.ActivityActive, LastActivityAt: now, Source: domain.SourceHook,
			},
		},
		Metadata:  domain.SessionMetadata{Branch: branch, WorkspacePath: "/workspace/" + branch},
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// ---- tests ----

// TestHappyPath drives Spawn -> SCM PR observation (open + CI passing) -> Kill,
// asserting via direct store reads that the canonical row, the PR row, and the
// change_log stream all reflect what each step contributed.
func TestHappyPath_Spawn_PR_Kill(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := openLiveStack(t, t.TempDir())
	defer st.close(t)
	seedProject(t, st.store, "mer")

	// 1. Spawn — SM inserts the session row, LCM marks it live. We only assert
	//    the structural invariant of the id (project-scoped, non-empty), not the
	//    literal counter — that's a store-internal detail.
	sess, err := st.sm.Spawn(ctx, ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Prompt: "ship it",
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if sess.ID == "" || !strings.HasPrefix(string(sess.ID), "mer-") {
		t.Fatalf("expected project-scoped id like mer-N, got %q", sess.ID)
	}

	rec, ok, err := st.store.GetSession(ctx, sess.ID)
	if err != nil || !ok {
		t.Fatalf("get session: ok=%v err=%v", ok, err)
	}
	if !rec.Lifecycle.IsAlive {
		t.Fatal("post-spawn: is_alive should be true")
	}
	if rec.Lifecycle.Session.State != domain.SessionNotStarted {
		t.Fatalf("post-spawn state want not_started, got %q", rec.Lifecycle.Session.State)
	}
	if rec.Metadata.RuntimeHandleID != "h1" || rec.Metadata.RuntimeName != "tmux" {
		t.Fatalf("post-spawn handles missing: %+v", rec.Metadata)
	}
	if rec.Metadata.WorkspacePath == "" || rec.Metadata.Prompt != "ship it" {
		t.Fatalf("post-spawn metadata missing: %+v", rec.Metadata)
	}

	// 2. SCM observes a fresh PR — open, CI passing. LCM writes the pr row
	//    atomically (one tx, triggers fire pr_created).
	prURL := "https://github.com/repo/mer/pull/1"
	if err := st.lcm.ApplyPRObservation(ctx, sess.ID, ports.PRObservation{
		Fetched: true, URL: prURL, Number: 1,
		CI: domain.CIPassing, Review: domain.ReviewNone, Mergeability: domain.MergeMergeable,
		Checks: []domain.PRCheckRow{{
			Name: "ci/build", CommitHash: "abc123", Status: "passed", CreatedAt: time.Now(),
		}},
	}); err != nil {
		t.Fatalf("apply pr: %v", err)
	}
	prRow, ok, err := st.store.GetPR(ctx, prURL)
	if err != nil || !ok {
		t.Fatalf("get pr: ok=%v err=%v", ok, err)
	}
	if prRow.SessionID != string(sess.ID) || prRow.CI != domain.CIPassing || prRow.Draft || prRow.Merged || prRow.Closed {
		t.Fatalf("pr row wrong: %+v", prRow)
	}

	// 3. Kill — SM routes to LCM and tears down runtime+workspace.
	freed, err := st.sm.Kill(ctx, sess.ID, domain.TermManuallyKilled)
	if err != nil || !freed {
		t.Fatalf("kill freed=%v err=%v", freed, err)
	}
	rec, _, _ = st.store.GetSession(ctx, sess.ID)
	if rec.Lifecycle.Session.State != domain.SessionTerminated ||
		rec.Lifecycle.TerminationReason != domain.TermManuallyKilled ||
		rec.Lifecycle.IsAlive {
		t.Fatalf("post-kill canonical wrong: %+v", rec.Lifecycle)
	}

	// 4. Assert the change_log captured the full timeline. The DB triggers
	//    write the only durable CDC; we don't want to assume an ordering of
	//    interleaved events, just that each expected event_type shows up.
	rows, err := st.store.ReadChangeLogAfter(ctx, 0, 100)
	if err != nil {
		t.Fatalf("read change_log: %v", err)
	}
	seen := map[string]bool{}
	for _, r := range rows {
		seen[r.EventType] = true
	}
	for _, want := range []string{"session_created", "session_updated", "pr_created", "pr_check_recorded"} {
		if !seen[want] {
			t.Fatalf("missing change_log event %q (got: %v)", want, seen)
		}
	}
}

// TestRestoreRoundTrip simulates a daemon restart: spawn a session, persist the
// kill, fully close the in-process LCM/SM, open a fresh stack against the SAME
// DB file, and Restore. The restored session must keep its metadata (the agent
// session id is the must-survive bit).
func TestRestoreRoundTrip_PreservesMetadata(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	st := openLiveStack(t, dir)
	seedProject(t, st.store, "mer")

	// Phase A: spawn with an agent session id, then kill so the row is terminal
	// and Restore is legal.
	sess, err := st.sm.Spawn(ctx, ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Prompt: "remember me",
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	// fold an AgentSessionID into the row — the LCM does this through the spawn
	// outcome on Restore too, but a fresh spawn doesn't (the agent has not
	// reported one yet). We patch via the store so the restore branch has
	// something to resume from. Check ok/err: without it, a missed row would
	// hand UpdateSession a zero-value record (ID==""), which matches no rows
	// and returns nil — Phase B would then fail with a misleading "agent id
	// lost across restart" rather than the real cause.
	rec, ok, err := st.store.GetSession(ctx, sess.ID)
	if err != nil || !ok {
		t.Fatalf("get session for patch: ok=%v err=%v", ok, err)
	}
	rec.Metadata.AgentSessionID = "agent-xyz"
	if err := st.store.UpdateSession(ctx, rec); err != nil {
		t.Fatalf("patch agent id: %v", err)
	}
	if _, err := st.sm.Kill(ctx, sess.ID, domain.TermManuallyKilled); err != nil {
		t.Fatalf("kill: %v", err)
	}
	st.close(t)

	// Phase B: reopen against the same data dir; everything in memory is gone.
	st2 := openLiveStack(t, dir)
	defer st2.close(t)

	// Confirm the row survived the restart.
	rec2, ok, err := st2.store.GetSession(ctx, sess.ID)
	if err != nil || !ok {
		t.Fatalf("reopen get: ok=%v err=%v", ok, err)
	}
	if rec2.Metadata.AgentSessionID != "agent-xyz" {
		t.Fatalf("agent session id lost across restart: %+v", rec2.Metadata)
	}
	if rec2.Lifecycle.Session.State != domain.SessionTerminated {
		t.Fatalf("expected terminal after reopen, got %q", rec2.Lifecycle.Session.State)
	}

	// Phase C: Restore — must drive a fresh OnSpawnCompleted and surface the
	// preserved AgentSessionID into the new outcome.
	restored, err := st2.sm.Restore(ctx, sess.ID)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if !restored.Lifecycle.IsAlive {
		t.Fatal("restored session should be is_alive after spawn-completed")
	}
	if restored.Metadata.AgentSessionID != "agent-xyz" {
		t.Fatalf("restored row dropped AgentSessionID: %+v", restored.Metadata)
	}
}

// TestCIFailureAndRecovery drives the CI-failed reaction path: a failing
// observation injects a nudge into the agent (messenger), a recovery
// observation (CI passing) flips state without re-firing the nudge, and the
// pr_checks history records both runs so the brake's "last 3 all failed" query
// reads the truth.
func TestCIFailureAndRecovery_NudgeThenClears(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := openLiveStack(t, t.TempDir())
	defer st.close(t)
	seedProject(t, st.store, "mer")

	sess, err := st.sm.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Prompt: "."})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	// Move the session out of not_started so the reaction path engages on real
	// PR facts (not_started doesn't react on PRs).
	if err := st.lcm.ApplyActivitySignal(ctx, sess.ID, ports.ActivitySignal{
		Valid: true, State: domain.ActivityActive, Source: domain.SourceHook, Timestamp: time.Now(),
	}); err != nil {
		t.Fatalf("activity: %v", err)
	}
	_ = st.messenger.drain() // ignore startup nudges, focus on CI

	prURL := "https://github.com/repo/mer/pull/2"
	// Failing CI: handleCIFailure should send a CI-failed nudge with the log
	// tail injected.
	if err := st.lcm.ApplyPRObservation(ctx, sess.ID, ports.PRObservation{
		Fetched: true, URL: prURL, Number: 2,
		CI: domain.CIFailing, Mergeability: domain.MergeUnstable,
		Checks: []domain.PRCheckRow{{
			Name: "ci/build", CommitHash: "c1", Status: "failed", LogTail: "panic: nil map", CreatedAt: time.Now(),
		}},
	}); err != nil {
		t.Fatalf("apply pr (failing): %v", err)
	}
	got := st.messenger.drain()
	if len(got) == 0 {
		t.Fatal("expected CI-failed nudge to the agent")
	}
	if !strings.Contains(got[0], "CI is failing") || !strings.Contains(got[0], "panic: nil map") {
		t.Fatalf("ci-failed message missing content: %q", got[0])
	}

	// Brake confirmation: only one failure so far, RecentCheckStatuses should
	// reflect it.
	history, err := st.store.RecentCheckStatuses(ctx, prURL, "ci/build", 3)
	if err != nil {
		t.Fatalf("recent checks: %v", err)
	}
	if len(history) != 1 || history[0] != "failed" {
		t.Fatalf("ci history wrong: %v", history)
	}

	// Recovery: CI passing on a new commit. With the dedupe slot still on
	// rxCIFailed, the dispatch path moves to rxApprovedGreen (mergeable) and
	// the human notifier is the one that pages.
	if err := st.lcm.ApplyPRObservation(ctx, sess.ID, ports.PRObservation{
		Fetched: true, URL: prURL, Number: 2,
		CI: domain.CIPassing, Mergeability: domain.MergeMergeable,
		Checks: []domain.PRCheckRow{{
			Name: "ci/build", CommitHash: "c2", Status: "passed", CreatedAt: time.Now(),
		}},
	}); err != nil {
		t.Fatalf("apply pr (recovery): %v", err)
	}
	ev := st.notifier.drain()
	if len(ev) == 0 {
		t.Fatal("recovery: notifier should have received an event (approved-and-green)")
	}
	if !anyEventType(ev, "reaction.approved-and-green") {
		t.Fatalf("recovery should notify approved-and-green, got %+v", ev)
	}

	// And the pr row reflects the recovery in the canonical fact store.
	prRow, ok, _ := st.store.GetPR(ctx, prURL)
	if !ok || prRow.CI != domain.CIPassing {
		t.Fatalf("pr ci_state should be passing post-recovery: %+v", prRow)
	}
}

// TestDetectingPersistsAcrossRestart drives the runtime quarantine path: a
// failed probe puts the session into the detecting state, which means the
// decider's anti-flap memory MUST be flushed to the detecting_* columns and
// survive a restart. A subsequent alive probe must clear it.
func TestDetectingPersistsAcrossRestart(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	st := openLiveStack(t, dir)
	seedProject(t, st.store, "mer")

	sess, err := st.sm.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Prompt: "."})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	// Move to working so the runtime decider doesn't bail on not_started.
	if err := st.lcm.ApplyActivitySignal(ctx, sess.ID, ports.ActivitySignal{
		Valid: true, State: domain.ActivityActive, Source: domain.SourceHook, Timestamp: time.Now(),
	}); err != nil {
		t.Fatalf("activity: %v", err)
	}
	// One failed probe should park the session in detecting with attempts=1.
	if err := st.lcm.ApplyRuntimeObservation(ctx, sess.ID, ports.RuntimeFacts{
		ObservedAt: time.Now(),
		Runtime:    ports.ProbeFailed,
		Process:    ports.ProbeFailed,
	}); err != nil {
		t.Fatalf("apply runtime: %v", err)
	}
	rec, ok, err := st.store.GetSession(ctx, sess.ID)
	if err != nil || !ok {
		t.Fatalf("get session post-probe: ok=%v err=%v", ok, err)
	}
	if rec.Lifecycle.Session.State != domain.SessionDetecting {
		t.Fatalf("expected detecting state, got %q", rec.Lifecycle.Session.State)
	}
	if rec.Lifecycle.Detecting == nil || rec.Lifecycle.Detecting.Attempts == 0 {
		t.Fatalf("detecting memory should be populated: %+v", rec.Lifecycle.Detecting)
	}

	// Restart: close, reopen, verify the detecting_* columns round-tripped.
	st.close(t)
	st2 := openLiveStack(t, dir)
	defer st2.close(t)

	rec2, ok, _ := st2.store.GetSession(ctx, sess.ID)
	if !ok || rec2.Lifecycle.Detecting == nil {
		t.Fatalf("detecting lost across restart: %+v", rec2.Lifecycle)
	}
	if rec2.Lifecycle.Detecting.Attempts != rec.Lifecycle.Detecting.Attempts {
		t.Fatalf("attempts round-trip mismatch: pre=%d post=%d",
			rec.Lifecycle.Detecting.Attempts, rec2.Lifecycle.Detecting.Attempts)
	}
	if rec2.Lifecycle.Detecting.EvidenceHash != rec.Lifecycle.Detecting.EvidenceHash {
		t.Fatal("evidence hash dropped across restart")
	}

	// Recovery probe — alive — must clear detecting and flip state out of it.
	if err := st2.lcm.ApplyRuntimeObservation(ctx, sess.ID, ports.RuntimeFacts{
		ObservedAt: time.Now(),
		Runtime:    ports.ProbeAlive,
		Process:    ports.ProbeAlive,
	}); err != nil {
		t.Fatalf("recovery probe: %v", err)
	}
	rec3, ok3, err := st2.store.GetSession(ctx, sess.ID)
	if err != nil || !ok3 {
		t.Fatalf("get session post-recovery: ok=%v err=%v", ok3, err)
	}
	if rec3.Lifecycle.Detecting != nil {
		t.Fatalf("alive probe should clear detecting, got %+v", rec3.Lifecycle.Detecting)
	}
	if rec3.Lifecycle.Session.State == domain.SessionDetecting {
		t.Fatalf("session state should leave detecting, got %q", rec3.Lifecycle.Session.State)
	}
}

// TestCDCPollerReceivesAllStages drives the full real pipeline including the
// in-process CDC poller — proving the trigger writes become broadcaster events
// in the same order the storage layer observes them.
func TestCDCPollerReceivesAllStages(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := openLiveStack(t, t.TempDir())
	defer st.close(t)
	seedProject(t, st.store, "mer")

	bcast := cdc.NewBroadcaster()
	src := pollerSource{st.store}
	poller := cdc.NewPoller(src, bcast, cdc.PollerConfig{Batch: 100})

	var (
		mu     sync.Mutex
		events []cdc.Event
	)
	bcast.Subscribe(func(e cdc.Event) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, e)
	})

	sess, err := st.sm.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Prompt: "."})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if err := st.lcm.ApplyActivitySignal(ctx, sess.ID, ports.ActivitySignal{
		Valid: true, State: domain.ActivityActive, Source: domain.SourceHook, Timestamp: time.Now(),
	}); err != nil {
		t.Fatalf("activity: %v", err)
	}
	if err := st.lcm.ApplyPRObservation(ctx, sess.ID, ports.PRObservation{
		Fetched: true, URL: "https://github.com/repo/mer/pull/3", Number: 3,
		CI: domain.CIPassing, Mergeability: domain.MergeMergeable,
	}); err != nil {
		t.Fatalf("apply pr: %v", err)
	}

	if err := poller.Poll(ctx); err != nil {
		t.Fatalf("poll: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	types := map[cdc.EventType]bool{}
	for _, e := range events {
		types[e.Type] = true
	}
	for _, want := range []cdc.EventType{cdc.EventSessionCreated, cdc.EventSessionUpdated, cdc.EventPRCreated} {
		if !types[want] {
			t.Fatalf("poller missed event %q (got %+v)", want, types)
		}
	}
	// Seq monotonicity invariant — the wiring assumes it; assert it here.
	var prev int64
	for _, e := range events {
		if e.Seq <= prev {
			t.Fatalf("seq not monotonic: %d after %d", e.Seq, prev)
		}
		prev = e.Seq
	}
}

func TestLifecycleDurableNotification_NeedsInput(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer store.Close()
	seedProject(t, store, "mer")
	rec, err := store.CreateSession(ctx, durableRecord("mer", "MER-1", "feat/input"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	lcm := durableLifecycle(store, &captureMessenger{})
	startSeq, _ := store.MaxChangeLogSeq(ctx)

	if err := lcm.ApplyActivitySignal(ctx, rec.ID, ports.ActivitySignal{
		Valid: true, State: domain.ActivityWaitingInput, Source: domain.SourceHook, Timestamp: time.Now(),
	}); err != nil {
		t.Fatalf("activity: %v", err)
	}

	notifications, err := store.ListNotifications(ctx, sqlite.NotificationFilter{SessionID: string(rec.ID), Limit: 10})
	if err != nil {
		t.Fatalf("list notifications: %v", err)
	}
	if len(notifications) != 1 || notifications[0].SemanticType != "session.needs_input" || notifications[0].DedupeKey == "" {
		t.Fatalf("needs_input notification missing: %+v", notifications)
	}
	assertNotificationCreatedCDC(t, store, startSeq)
}

func TestLifecycleDurableNotification_ApprovedAndGreen(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer store.Close()
	seedProject(t, store, "mer")
	rec, err := store.CreateSession(ctx, durableRecord("mer", "MER-2", "feat/green"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	lcm := durableLifecycle(store, &captureMessenger{})

	if err := lcm.ApplyPRObservation(ctx, rec.ID, ports.PRObservation{
		Fetched: true, URL: "https://github.com/org/repo/pull/2", Number: 2,
		CI: domain.CIPassing, Review: domain.ReviewApproved, Mergeability: domain.MergeMergeable,
	}); err != nil {
		t.Fatalf("apply pr: %v", err)
	}
	notifications, err := store.ListNotifications(ctx, sqlite.NotificationFilter{SessionID: string(rec.ID), Limit: 10})
	if err != nil {
		t.Fatalf("list notifications: %v", err)
	}
	if len(notifications) != 1 || notifications[0].SemanticType != "merge.ready" {
		t.Fatalf("approved-and-green notification missing: %+v", notifications)
	}
}

func TestLifecycleDurableNotification_PRMerged(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer store.Close()
	seedProject(t, store, "mer")
	rec, err := store.CreateSession(ctx, durableRecord("mer", "MER-3", "feat/merge"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	lcm := durableLifecycle(store, &captureMessenger{})
	startSeq, _ := store.MaxChangeLogSeq(ctx)

	if err := lcm.ApplyPRObservation(ctx, rec.ID, ports.PRObservation{
		Fetched: true, URL: "https://github.com/org/repo/pull/3", Number: 3, Merged: true,
		CI: domain.CIPassing, Review: domain.ReviewApproved, Mergeability: domain.MergeMergeable,
	}); err != nil {
		t.Fatalf("apply pr: %v", err)
	}
	notifications, err := store.ListNotifications(ctx, sqlite.NotificationFilter{SessionID: string(rec.ID), Limit: 10})
	if err != nil {
		t.Fatalf("list notifications: %v", err)
	}
	if len(notifications) != 1 || notifications[0].SemanticType != "pr.merged" {
		t.Fatalf("pr_merged notification missing: %+v", notifications)
	}
	assertNotificationCreatedCDC(t, store, startSeq)
}

func assertNotificationCreatedCDC(t *testing.T, store *sqlite.Store, after int64) {
	t.Helper()
	evs, err := store.ReadChangeLogAfter(context.Background(), after, 20)
	if err != nil {
		t.Fatalf("read change_log: %v", err)
	}
	for _, e := range evs {
		if e.EventType == string(cdc.EventNotificationCreated) {
			return
		}
	}
	t.Fatalf("missing notification_created CDC after %d: %+v", after, evs)
}

// ---- small helpers ----

type pollerSource struct{ *sqlite.Store }

func (s pollerSource) EventsAfter(ctx context.Context, after int64, limit int) ([]cdc.Event, error) {
	rows, err := s.ReadChangeLogAfter(ctx, after, limit)
	if err != nil {
		return nil, err
	}
	out := make([]cdc.Event, len(rows))
	for i, r := range rows {
		out[i] = cdc.Event{
			Seq:       r.Seq,
			ProjectID: r.ProjectID,
			SessionID: r.SessionID,
			Type:      cdc.EventType(r.EventType),
			Payload:   []byte(r.Payload),
			CreatedAt: r.CreatedAt,
		}
	}
	return out, nil
}
func (s pollerSource) LatestSeq(ctx context.Context) (int64, error) {
	return s.MaxChangeLogSeq(ctx)
}

func anyEventType(evs []ports.Event, t string) bool {
	for _, e := range evs {
		if e.Type == t {
			return true
		}
	}
	return false
}
