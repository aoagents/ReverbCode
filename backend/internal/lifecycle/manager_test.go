package lifecycle

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var t0 = time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)

const sid domain.SessionID = "s1"

func newManager() (*Manager, *fakeStore) {
	store := newFakeStore()
	return New(store, &recordingNotifier{}, &recordingMessenger{}), store
}

func mustLoad(t *testing.T, store *fakeStore) domain.CanonicalSessionLifecycle {
	t.Helper()
	l, ok, err := store.Load(context.Background(), sid)
	if err != nil || !ok {
		t.Fatalf("load: ok=%v err=%v", ok, err)
	}
	return l
}

// ---- ApplyRuntimeObservation + #1 composition + #3 detecting clear ----

func TestApplyRuntimeObservation(t *testing.T) {
	aliveProbe := ports.RuntimeFacts{RuntimeState: ports.RuntimeProbeAlive, ProcessState: ports.ProcessProbeAlive, ObservedAt: t0}
	failedProbe := ports.RuntimeFacts{RuntimeState: ports.RuntimeProbeFailed, ProcessState: ports.ProcessProbeAlive, ObservedAt: t0}
	deadProbe := ports.RuntimeFacts{RuntimeState: ports.RuntimeProbeDead, ProcessState: ports.ProcessProbeDead, ObservedAt: t0}

	tests := []struct {
		name          string
		seed          domain.CanonicalSessionLifecycle
		facts         ports.RuntimeFacts
		wantSession   domain.SessionState
		wantReason    domain.SessionReason
		wantRuntime   domain.RuntimeState
		wantDisplay   domain.SessionStatus
		wantDetecting bool // expect non-nil detecting memory persisted
	}{
		{
			name:          "healthy probe must not clobber an activity-owned needs_input (#1)",
			seed:          lc(domain.SessionNeedsInput, domain.ReasonAwaitingUserInput, domain.RuntimeAlive),
			facts:         aliveProbe,
			wantSession:   domain.SessionNeedsInput,
			wantReason:    domain.ReasonAwaitingUserInput,
			wantRuntime:   domain.RuntimeAlive,
			wantDisplay:   domain.StatusNeedsInput,
			wantDetecting: false,
		},
		{
			name:          "healthy probe recovers a liveness-owned detecting -> working and clears memory (#1 + #3)",
			seed:          detectingLC(),
			facts:         aliveProbe,
			wantSession:   domain.SessionWorking,
			wantReason:    domain.ReasonTaskInProgress,
			wantRuntime:   domain.RuntimeAlive,
			wantDisplay:   domain.StatusWorking,
			wantDetecting: false,
		},
		{
			name:          "failed probe routes to detecting and records memory",
			seed:          lc(domain.SessionWorking, domain.ReasonTaskInProgress, domain.RuntimeAlive),
			facts:         failedProbe,
			wantSession:   domain.SessionDetecting,
			wantReason:    domain.ReasonProbeFailure,
			wantRuntime:   domain.RuntimeProbeFailed,
			wantDisplay:   domain.StatusDetecting,
			wantDetecting: true,
		},
		{
			name:          "dead+dead with no recent activity concludes killed and clears detecting (#3)",
			seed:          detectingLC(),
			facts:         deadProbe,
			wantSession:   domain.SessionTerminated,
			wantReason:    domain.ReasonRuntimeLost,
			wantRuntime:   domain.RuntimeExited,
			wantDisplay:   domain.StatusKilled,
			wantDetecting: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mgr, store := newManager()
			store.seed(sid, tt.seed)

			if err := mgr.ApplyRuntimeObservation(context.Background(), sid, tt.facts); err != nil {
				t.Fatalf("apply: %v", err)
			}

			l := mustLoad(t, store)
			if l.Session.State != tt.wantSession || l.Session.Reason != tt.wantReason {
				t.Errorf("session = %v/%v, want %v/%v", l.Session.State, l.Session.Reason, tt.wantSession, tt.wantReason)
			}
			if l.Runtime.State != tt.wantRuntime {
				t.Errorf("runtime = %v, want %v", l.Runtime.State, tt.wantRuntime)
			}
			if got := domain.DeriveLegacyStatus(l); got != tt.wantDisplay {
				t.Errorf("display = %v, want %v", got, tt.wantDisplay)
			}
			if (l.Detecting != nil) != tt.wantDetecting {
				t.Errorf("detecting present = %v, want %v (%+v)", l.Detecting != nil, tt.wantDetecting, l.Detecting)
			}
		})
	}
}

func TestApplyRuntimeObservation_NoRecordIsNoOp(t *testing.T) {
	mgr, store := newManager()
	if err := mgr.ApplyRuntimeObservation(context.Background(), sid, ports.RuntimeFacts{RuntimeState: ports.RuntimeProbeAlive, ProcessState: ports.ProcessProbeAlive, ObservedAt: t0}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, ok, _ := store.Load(context.Background(), sid); ok {
		t.Error("a probe for an unseeded session must not fabricate a record")
	}
}

func TestApplyRuntimeObservation_DoesNotResurrectTerminal(t *testing.T) {
	mgr, store := newManager()
	store.seed(sid, lc(domain.SessionTerminated, domain.ReasonManuallyKilled, domain.RuntimeExited))

	// A failed probe would normally route to detecting, but a terminal session
	// must not be reopened by an observation (only an explicit Restore does).
	if err := mgr.ApplyRuntimeObservation(context.Background(), sid, ports.RuntimeFacts{RuntimeState: ports.RuntimeProbeFailed, ProcessState: ports.ProcessProbeAlive, ObservedAt: t0}); err != nil {
		t.Fatalf("apply: %v", err)
	}

	l := mustLoad(t, store)
	if l.Session.State != domain.SessionTerminated || l.Session.Reason != domain.ReasonManuallyKilled {
		t.Errorf("session = %v/%v, want terminated/manually_killed (no resurrection)", l.Session.State, l.Session.Reason)
	}
	if l.Detecting != nil {
		t.Errorf("terminal session must not gain detecting memory, got %+v", l.Detecting)
	}
}

// ---- ApplyActivitySignal ----

func TestApplyActivitySignal(t *testing.T) {
	tests := []struct {
		name         string
		seed         domain.CanonicalSessionLifecycle
		signal       ports.ActivitySignal
		wantSession  domain.SessionState
		wantReason   domain.SessionReason
		checkReason  bool
		wantActivity domain.ActivityState
		wantChanged  bool
	}{
		{
			name:         "valid waiting_input maps to needs_input",
			seed:         lc(domain.SessionWorking, domain.ReasonTaskInProgress, domain.RuntimeAlive),
			signal:       ports.ActivitySignal{State: ports.SignalValid, Activity: domain.ActivityWaitingInput, Timestamp: t0, Source: domain.SourceHook},
			wantSession:  domain.SessionNeedsInput,
			wantActivity: domain.ActivityWaitingInput,
			wantChanged:  true,
		},
		{
			name:         "valid active recovers needs_input -> working",
			seed:         lc(domain.SessionNeedsInput, domain.ReasonAwaitingUserInput, domain.RuntimeAlive),
			signal:       ports.ActivitySignal{State: ports.SignalValid, Activity: domain.ActivityActive, Timestamp: t0, Source: domain.SourceHook},
			wantSession:  domain.SessionWorking,
			wantActivity: domain.ActivityActive,
			wantChanged:  true,
		},
		{
			name:         "valid idle maps to idle with a neutral reason",
			seed:         lc(domain.SessionWorking, domain.ReasonTaskInProgress, domain.RuntimeAlive),
			signal:       ports.ActivitySignal{State: ports.SignalValid, Activity: domain.ActivityIdle, Timestamp: t0, Source: domain.SourceHook},
			wantSession:  domain.SessionIdle,
			wantReason:   "",
			checkReason:  true,
			wantActivity: domain.ActivityIdle,
			wantChanged:  true,
		},
		{
			name:        "low-confidence signal is dropped (no idleness inferred)",
			seed:        lc(domain.SessionWorking, domain.ReasonTaskInProgress, domain.RuntimeAlive),
			signal:      ports.ActivitySignal{State: ports.SignalProbeFailure, Activity: domain.ActivityIdle, Timestamp: t0, Source: domain.SourceHook},
			wantSession: domain.SessionWorking,
			wantChanged: false,
		},
		{
			name:         "valid activity resolves a detecting session (proof of life)",
			seed:         detectingLC(),
			signal:       ports.ActivitySignal{State: ports.SignalValid, Activity: domain.ActivityActive, Timestamp: t0, Source: domain.SourceHook},
			wantSession:  domain.SessionWorking,
			wantActivity: domain.ActivityActive,
			wantChanged:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mgr, store := newManager()
			store.seed(sid, tt.seed)

			if err := mgr.ApplyActivitySignal(context.Background(), sid, tt.signal); err != nil {
				t.Fatalf("apply: %v", err)
			}

			l := mustLoad(t, store)
			if l.Session.State != tt.wantSession {
				t.Errorf("session = %v, want %v", l.Session.State, tt.wantSession)
			}
			if tt.checkReason && l.Session.Reason != tt.wantReason {
				t.Errorf("session reason = %q, want %q", l.Session.Reason, tt.wantReason)
			}
			if tt.wantChanged && l.Revision != 1 {
				t.Errorf("revision = %d, want 1 (expected a write)", l.Revision)
			}
			if !tt.wantChanged && l.Revision != 0 {
				t.Errorf("revision = %d, want 0 (expected a no-op)", l.Revision)
			}
			if tt.wantChanged && tt.wantActivity != "" && l.Activity.State != tt.wantActivity {
				t.Errorf("activity = %v, want %v", l.Activity.State, tt.wantActivity)
			}
			if tt.name == "valid activity resolves a detecting session (proof of life)" && l.Detecting != nil {
				t.Errorf("resolving detecting must clear the quarantine memory, got %+v", l.Detecting)
			}
		})
	}
}

// ---- ApplySCMObservation ----

func TestApplySCMObservation(t *testing.T) {
	t.Run("failed fetch is a no-op (failed probe != no PR)", func(t *testing.T) {
		mgr, store := newManager()
		store.seed(sid, lc(domain.SessionWorking, domain.ReasonTaskInProgress, domain.RuntimeAlive))
		if err := mgr.ApplySCMObservation(context.Background(), sid, ports.SCMFacts{Fetched: false, PRState: domain.PROpen}); err != nil {
			t.Fatalf("apply: %v", err)
		}
		if l := mustLoad(t, store); l.Revision != 0 || l.PR.State != "" {
			t.Errorf("expected no-op, got revision=%d pr=%v", l.Revision, l.PR.State)
		}
	})

	t.Run("open PR writes only the PR axis; session stays activity-owned", func(t *testing.T) {
		mgr, store := newManager()
		store.seed(sid, lc(domain.SessionWorking, domain.ReasonTaskInProgress, domain.RuntimeAlive))
		f := ports.SCMFacts{Fetched: true, PRState: domain.PROpen, CISummary: ports.CIFailing, PRNumber: 12, PRURL: "https://x/12"}
		if err := mgr.ApplySCMObservation(context.Background(), sid, f); err != nil {
			t.Fatalf("apply: %v", err)
		}
		l := mustLoad(t, store)
		if l.PR.State != domain.PROpen || l.PR.Reason != domain.PRReasonCIFailing || l.PR.Number != 12 {
			t.Errorf("pr = %+v, want open/ci_failing/#12", l.PR)
		}
		if l.Session.State != domain.SessionWorking {
			t.Errorf("session = %v, want working (untouched)", l.Session.State)
		}
		if got := domain.DeriveLegacyStatus(l); got != domain.StatusCIFailed {
			t.Errorf("display = %v, want ci_failed", got)
		}
	})

	t.Run("draft PR writes draft or ci_failed without review states", func(t *testing.T) {
		cases := []struct {
			name       string
			facts      ports.SCMFacts
			wantReason domain.PRReason
			wantStatus domain.SessionStatus
		}{
			{"draft with failing CI", ports.SCMFacts{Fetched: true, PRState: domain.PRDraft, CISummary: ports.CIFailing}, domain.PRReasonCIFailing, domain.StatusCIFailed},
			{"draft via bool with open state", ports.SCMFacts{Fetched: true, PRState: domain.PROpen, Draft: true}, domain.PRReasonInProgress, domain.StatusDraft},
			{"draft via bool with failing CI", ports.SCMFacts{Fetched: true, PRState: domain.PROpen, Draft: true, CISummary: ports.CIFailing}, domain.PRReasonCIFailing, domain.StatusCIFailed},
			{"draft ignores review and merge facts", ports.SCMFacts{Fetched: true, PRState: domain.PRDraft, ReviewDecision: ports.ReviewApproved, Mergeability: ports.Mergeability{Mergeable: true}}, domain.PRReasonInProgress, domain.StatusDraft},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				mgr, store := newManager()
				wantSession := domain.SessionSubstate{State: domain.SessionWorking, Reason: domain.ReasonTaskInProgress}
				store.seed(sid, lc(wantSession.State, wantSession.Reason, domain.RuntimeAlive))
				if err := mgr.ApplySCMObservation(context.Background(), sid, c.facts); err != nil {
					t.Fatalf("apply: %v", err)
				}
				l := mustLoad(t, store)
				if l.PR.State != domain.PRDraft || l.PR.Reason != c.wantReason {
					t.Errorf("pr = %v/%v, want draft/%v", l.PR.State, l.PR.Reason, c.wantReason)
				}
				if l.Session != wantSession {
					t.Errorf("session = %+v, want untouched %+v", l.Session, wantSession)
				}
				if got := domain.DeriveLegacyStatus(l); got != c.wantStatus {
					t.Errorf("display = %v, want %v", got, c.wantStatus)
				}
			})
		}
	})

	t.Run("merged PR parks the session and displays merged", func(t *testing.T) {
		mgr, store := newManager()
		seed := lc(domain.SessionWorking, domain.ReasonTaskInProgress, domain.RuntimeAlive)
		seed.PR = domain.PRSubstate{State: domain.PROpen, Reason: domain.PRReasonInProgress, Number: 12}
		store.seed(sid, seed)
		f := ports.SCMFacts{Fetched: true, PRState: domain.PRMerged, PRNumber: 12}
		if err := mgr.ApplySCMObservation(context.Background(), sid, f); err != nil {
			t.Fatalf("apply: %v", err)
		}
		l := mustLoad(t, store)
		if l.PR.State != domain.PRMerged || l.Session.Reason != domain.ReasonMergedWaitingDecision {
			t.Errorf("got pr=%v session=%v, want merged + merged_waiting_decision", l.PR.State, l.Session.Reason)
		}
		if got := domain.DeriveLegacyStatus(l); got != domain.StatusMerged {
			t.Errorf("display = %v, want merged", got)
		}
	})

	t.Run("open-PR review branches map to the PR axis", func(t *testing.T) {
		cases := []struct {
			name       string
			facts      ports.SCMFacts
			wantReason domain.PRReason
			wantStatus domain.SessionStatus
		}{
			{"changes requested", ports.SCMFacts{Fetched: true, PRState: domain.PROpen, ReviewDecision: ports.ReviewChangesRequested}, domain.PRReasonChangesRequested, domain.StatusChangesRequested},
			{"pending human comments", ports.SCMFacts{Fetched: true, PRState: domain.PROpen, PendingComments: []ports.ReviewComment{{Author: "human", Body: "fix"}}}, domain.PRReasonChangesRequested, domain.StatusChangesRequested},
			{"pending bot comments", ports.SCMFacts{Fetched: true, PRState: domain.PROpen, PendingComments: []ports.ReviewComment{{Author: "bot", Body: "fix", IsBot: true}}}, domain.PRReasonBotComments, domain.StatusChangesRequested},
			{"merge conflicts", ports.SCMFacts{Fetched: true, PRState: domain.PROpen, Mergeability: ports.Mergeability{CIPassing: true, Approved: true, NoConflicts: false, Blockers: []string{"merge conflicts"}}}, domain.PRReasonMergeConflicts, domain.StatusPROpen},
			{"approved + mergeable", ports.SCMFacts{Fetched: true, PRState: domain.PROpen, ReviewDecision: ports.ReviewApproved, Mergeability: ports.Mergeability{Mergeable: true}}, domain.PRReasonMergeReady, domain.StatusMergeable},
			{"review pending", ports.SCMFacts{Fetched: true, PRState: domain.PROpen, ReviewDecision: ports.ReviewPending}, domain.PRReasonReviewPending, domain.StatusReviewPending},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				mgr, store := newManager()
				wantSession := domain.SessionSubstate{State: domain.SessionWorking, Reason: domain.ReasonTaskInProgress}
				store.seed(sid, lc(wantSession.State, wantSession.Reason, domain.RuntimeAlive))
				if err := mgr.ApplySCMObservation(context.Background(), sid, c.facts); err != nil {
					t.Fatalf("apply: %v", err)
				}
				l := mustLoad(t, store)
				if l.PR.State != domain.PROpen || l.PR.Reason != c.wantReason {
					t.Errorf("pr = %v/%v, want open/%v", l.PR.State, l.PR.Reason, c.wantReason)
				}
				if got := domain.DeriveLegacyStatus(l); got != c.wantStatus {
					t.Errorf("display = %v, want %v", got, c.wantStatus)
				}
			})
		}
	})

	t.Run("no PR is a no-op in split A", func(t *testing.T) {
		mgr, store := newManager()
		store.seed(sid, lc(domain.SessionWorking, domain.ReasonTaskInProgress, domain.RuntimeAlive))
		if err := mgr.ApplySCMObservation(context.Background(), sid, ports.SCMFacts{Fetched: true, PRState: domain.PRNone}); err != nil {
			t.Fatalf("apply: %v", err)
		}
		if l := mustLoad(t, store); l.Revision != 0 {
			t.Errorf("expected no-op, got revision=%d", l.Revision)
		}
	})
}

// ---- mutation outcomes ----

func TestOnSpawnCompleted(t *testing.T) {
	mgr, store := newManager()
	store.seed(sid, lc(domain.SessionNotStarted, domain.ReasonSpawnRequested, domain.RuntimeUnknown))

	out := ports.SpawnOutcome{
		Branch:         "feat/x",
		WorkspacePath:  "/w/x",
		RuntimeHandle:  ports.RuntimeHandle{ID: "tmux:1", RuntimeName: "tmux"},
		AgentSessionID: "agent-1",
	}
	if err := mgr.OnSpawnCompleted(context.Background(), sid, out); err != nil {
		t.Fatalf("apply: %v", err)
	}

	l := mustLoad(t, store)
	if l.Runtime.State != domain.RuntimeAlive {
		t.Errorf("runtime = %v, want alive", l.Runtime.State)
	}
	if l.Session.State != domain.SessionNotStarted {
		t.Errorf("session = %v, want not_started (spawn does not assert acknowledgement)", l.Session.State)
	}
	if got := domain.DeriveLegacyStatus(l); got != domain.StatusSpawning {
		t.Errorf("display = %v, want spawning", got)
	}
	meta, _ := store.GetMetadata(context.Background(), sid)
	if meta[MetaBranch] != "feat/x" || meta[MetaAgentSessionID] != "agent-1" || meta[MetaRuntimeName] != "tmux" {
		t.Errorf("metadata not recorded: %+v", meta)
	}
}

func TestOnSpawnInitiated_ActiveSessionRejected(t *testing.T) {
	mgr, store := newManager()
	store.seed(sid, lc(domain.SessionWorking, domain.ReasonTaskInProgress, domain.RuntimeAlive))

	err := mgr.OnSpawnInitiated(context.Background(), domain.SessionRecord{
		ID:        sid,
		ProjectID: domain.ProjectID("proj"),
		Lifecycle: lc(domain.SessionNotStarted, domain.ReasonSpawnRequested, domain.RuntimeUnknown),
	})
	if err == nil {
		t.Fatal("OnSpawnInitiated should reject a non-terminal row on top of an active session")
	}

	got := mustLoad(t, store)
	if got.Session.State != domain.SessionWorking || got.Revision != 0 {
		t.Fatalf("active row should be unchanged, got %+v", got)
	}
}

func TestOnKillRequested(t *testing.T) {
	tests := []struct {
		name        string
		kind        ports.LifecycleKillReason
		wantReason  domain.SessionReason
		wantRuntime domain.RuntimeReason
		wantDisplay domain.SessionStatus
	}{
		{"manual", ports.KillManual, domain.ReasonManuallyKilled, domain.RuntimeReasonManualKillRequested, domain.StatusKilled},
		{"cleanup", ports.KillCleanup, domain.ReasonAutoCleanup, domain.RuntimeReasonAutoCleanup, domain.StatusCleanup},
		{"error", ports.KillError, domain.ReasonErrorInProcess, domain.RuntimeReasonProbeError, domain.StatusErrored},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mgr, store := newManager()
			store.seed(sid, detectingLC())

			if err := mgr.OnKillRequested(context.Background(), sid, ports.KillReason{Kind: tt.kind, Detail: "x"}); err != nil {
				t.Fatalf("apply: %v", err)
			}

			l := mustLoad(t, store)
			if l.Session.State != domain.SessionTerminated || l.Session.Reason != tt.wantReason {
				t.Errorf("session = %v/%v, want terminated/%v", l.Session.State, l.Session.Reason, tt.wantReason)
			}
			if l.Runtime.Reason != tt.wantRuntime {
				t.Errorf("runtime reason = %v, want %v", l.Runtime.Reason, tt.wantRuntime)
			}
			if l.Detecting != nil {
				t.Errorf("kill must clear detecting memory, got %+v", l.Detecting)
			}
			if got := domain.DeriveLegacyStatus(l); got != tt.wantDisplay {
				t.Errorf("display = %v, want %v", got, tt.wantDisplay)
			}
		})
	}
}

func TestOnSpawnCompleted_UnseededErrors(t *testing.T) {
	mgr, store := newManager()
	err := mgr.OnSpawnCompleted(context.Background(), sid, ports.SpawnOutcome{Branch: "x"})
	if err == nil {
		t.Error("OnSpawnCompleted for an unseeded session must error, not fabricate a record")
	}
	if _, ok, _ := store.Load(context.Background(), sid); ok {
		t.Error("no record should have been created")
	}
}

func TestOnKillRequested_UnseededIsNoOp(t *testing.T) {
	mgr, store := newManager()
	if err := mgr.OnKillRequested(context.Background(), sid, ports.KillReason{Kind: ports.KillManual}); err != nil {
		t.Fatalf("kill of unknown session should be a benign no-op, got %v", err)
	}
	if _, ok, _ := store.Load(context.Background(), sid); ok {
		t.Error("killing an unknown session must not fabricate a terminal record")
	}
}

// ---- fake store contract ----

func TestFakeStoreUpsertFullRow(t *testing.T) {
	store := newFakeStore()
	store.seed(sid, lc(domain.SessionWorking, domain.ReasonTaskInProgress, domain.RuntimeAlive))

	rec, ok, err := store.Get(context.Background(), sid)
	if err != nil || !ok {
		t.Fatalf("seeded record missing: ok=%v err=%v", ok, err)
	}
	rec.Lifecycle.Session = domain.SessionSubstate{State: domain.SessionIdle, Reason: domain.ReasonResearchComplete}
	rec.Lifecycle.Runtime = domain.RuntimeSubstate{State: domain.RuntimeExited}
	if err := store.Upsert(context.Background(), rec, ports.EventSessionStateChanged); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, _, _ := store.Get(context.Background(), sid)
	if got.Lifecycle.Session.State != domain.SessionIdle || got.Lifecycle.Runtime.State != domain.RuntimeExited {
		t.Fatalf("upsert should replace the full canonical row, got %+v", got.Lifecycle)
	}
	if got.Lifecycle.Revision != 1 {
		t.Fatalf("upsert should bump revision inside the store, got %d want 1", got.Lifecycle.Revision)
	}
}

// ---- per-session serialisation under the race detector ----

func TestPerSessionSerialization(t *testing.T) {
	mgr, store := newManager()
	store.seed(sid, lc(domain.SessionWorking, domain.ReasonTaskInProgress, domain.RuntimeAlive))

	const n = 50
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			_ = mgr.ApplyActivitySignal(context.Background(), sid, ports.ActivitySignal{
				State:     ports.SignalValid,
				Activity:  domain.ActivityActive,
				Timestamp: t0.Add(time.Duration(i) * time.Second),
				Source:    domain.SourceHook,
			})
		}(i)
	}
	wg.Wait()

	// Each goroutine writes a distinct LastActivityAt, so every call is a real
	// change; with correct serialisation all n land without a lost update.
	if l := mustLoad(t, store); l.Revision != n {
		t.Errorf("revision = %d, want %d (lost update under concurrency)", l.Revision, n)
	}
}

// ---- RunningSessions (reaper poll-set) ----

func TestRunningSessions_NoListerWired_ReturnsEmpty(t *testing.T) {
	m, _ := newManager()
	got, err := m.RunningSessions(context.Background())
	if err != nil {
		t.Fatalf("RunningSessions: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty slice when no lister wired, got %d records", len(got))
	}
}

func TestRunningSessions_ListerErrorPropagates(t *testing.T) {
	m, _ := newManager()
	wantErr := errors.New("boom")
	m.WithSessionLister(func(_ context.Context) ([]domain.SessionRecord, error) {
		return nil, wantErr
	})
	_, err := m.RunningSessions(context.Background())
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected lister error to propagate, got %v", err)
	}
}

// TestRunningSessions_FilterIncludesProbableExcludesTerminal locks in the
// reaper poll-set predicate. The bug we are guarding against is filtering to
// "runtime.State == RuntimeAlive": detecting sessions (RuntimeMissing /
// RuntimeProbeFailed) would be silently parked, breaking the probe-driven
// recovery path proved by manager_test.go:59 and the dead+dead -> killed path
// proved by manager_test.go:79.
func TestRunningSessions_FilterIncludesProbableExcludesTerminal(t *testing.T) {
	m, _ := newManager()
	records := []domain.SessionRecord{
		{ID: "working-alive", Lifecycle: lc(domain.SessionWorking, domain.ReasonTaskInProgress, domain.RuntimeAlive)},
		{ID: "detecting-probefailed", Lifecycle: lc(domain.SessionDetecting, domain.ReasonProbeFailure, domain.RuntimeProbeFailed)},
		{ID: "detecting-missing", Lifecycle: lc(domain.SessionDetecting, domain.ReasonRuntimeLost, domain.RuntimeMissing)},
		{ID: "idle-alive", Lifecycle: lc(domain.SessionIdle, domain.ReasonResearchComplete, domain.RuntimeAlive)},
		{ID: "needs-input-alive", Lifecycle: lc(domain.SessionNeedsInput, domain.ReasonAwaitingUserInput, domain.RuntimeAlive)},
		{ID: "not-started", Lifecycle: lc(domain.SessionNotStarted, domain.ReasonSpawnRequested, domain.RuntimeUnknown)},
		{ID: "terminated", Lifecycle: lc(domain.SessionTerminated, domain.ReasonManuallyKilled, domain.RuntimeExited)},
		{ID: "done", Lifecycle: lc(domain.SessionDone, domain.ReasonPRMerged, domain.RuntimeExited)},
	}
	m.WithSessionLister(func(_ context.Context) ([]domain.SessionRecord, error) {
		return records, nil
	})

	got, err := m.RunningSessions(context.Background())
	if err != nil {
		t.Fatalf("RunningSessions: %v", err)
	}
	gotIDs := map[domain.SessionID]bool{}
	for _, r := range got {
		gotIDs[r.ID] = true
	}
	wantIncluded := []domain.SessionID{
		"working-alive", "detecting-probefailed", "detecting-missing",
		"idle-alive", "needs-input-alive", "not-started",
	}
	for _, id := range wantIncluded {
		if !gotIDs[id] {
			t.Errorf("expected %q in poll set, missing", id)
		}
	}
	wantExcluded := []domain.SessionID{"terminated", "done"}
	for _, id := range wantExcluded {
		if gotIDs[id] {
			t.Errorf("expected %q NOT in poll set, found", id)
		}
	}
}

// ---- helpers ----

func lc(state domain.SessionState, reason domain.SessionReason, rt domain.RuntimeState) domain.CanonicalSessionLifecycle {
	return domain.CanonicalSessionLifecycle{
		Version: domain.LifecycleVersion,
		Session: domain.SessionSubstate{State: state, Reason: reason},
		Runtime: domain.RuntimeSubstate{State: rt},
	}
}

func detectingLC() domain.CanonicalSessionLifecycle {
	l := lc(domain.SessionDetecting, domain.ReasonRuntimeLost, domain.RuntimeMissing)
	l.Detecting = &domain.DetectingState{Attempts: 1, StartedAt: t0, EvidenceHash: "abc"}
	return l
}
