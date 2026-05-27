package lifecycle

import (
	"context"
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
			{"approved + mergeable", ports.SCMFacts{Fetched: true, PRState: domain.PROpen, ReviewDecision: ports.ReviewApproved, Mergeability: ports.Mergeability{Mergeable: true}}, domain.PRReasonMergeReady, domain.StatusMergeable},
			{"review pending", ports.SCMFacts{Fetched: true, PRState: domain.PROpen, ReviewDecision: ports.ReviewPending}, domain.PRReasonReviewPending, domain.StatusReviewPending},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				mgr, store := newManager()
				store.seed(sid, lc(domain.SessionWorking, domain.ReasonTaskInProgress, domain.RuntimeAlive))
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

func TestFakeStoreExpectedRevision(t *testing.T) {
	store := newFakeStore()
	store.seed(sid, lc(domain.SessionWorking, domain.ReasonTaskInProgress, domain.RuntimeAlive)) // revision 0
	rt := domain.RuntimeSubstate{State: domain.RuntimeExited}

	bad := 99
	if err := store.PatchLifecycle(context.Background(), sid, ports.LifecyclePatch{Runtime: &rt, ExpectedRevision: &bad}); err == nil {
		t.Error("stale ExpectedRevision must be rejected")
	}
	good := 0
	if err := store.PatchLifecycle(context.Background(), sid, ports.LifecyclePatch{Runtime: &rt, ExpectedRevision: &good}); err != nil {
		t.Errorf("matching ExpectedRevision must succeed, got %v", err)
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
