package lifecycle

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// failingMessenger always fails delivery, counting attempts — used to assert a
// send failure does not consume escalation budget.
type failingMessenger struct{ attempts int }

func (f *failingMessenger) Send(_ context.Context, _ domain.SessionID, _ string) error {
	f.attempts++
	return fmt.Errorf("messenger unavailable")
}

// newReactive wires a Manager with handles on the recording fakes so reaction
// tests can assert what was sent/notified. clock is pinned to t0 for
// deterministic escalation stamping.
func newReactive() (*Manager, *fakeStore, *recordingNotifier, *recordingMessenger) {
	store := newFakeStore()
	notf := &recordingNotifier{}
	msgr := &recordingMessenger{}
	m := New(store, notf, msgr)
	m.clock = func() time.Time { return t0 }
	return m, store, notf, msgr
}

func lcOpenPR(reason domain.PRReason) domain.CanonicalSessionLifecycle {
	l := lc(domain.SessionWorking, domain.ReasonTaskInProgress, domain.RuntimeAlive)
	l.PR = domain.PRSubstate{State: domain.PROpen, Reason: reason, Number: 7}
	return l
}

func notifyCount(n *recordingNotifier, eventType string) int {
	n.mu.Lock()
	defer n.mu.Unlock()
	c := 0
	for _, e := range n.events {
		if e.Type == eventType {
			c++
		}
	}
	return c
}

func ctx() context.Context { return context.Background() }

// ---- right reaction per transition ----

func TestReaction_CIFailedSendsToAgentWithLogTail(t *testing.T) {
	m, store, notf, msgr := newReactive()
	store.seed(sid, lcOpenPR(domain.PRReasonReviewPending))

	tail := "build failed\nundefined: foo"
	err := m.ApplySCMObservation(ctx(), sid, ports.SCMFacts{
		Fetched: true, PRState: domain.PROpen, CISummary: ports.CIFailing,
		PRNumber: 7, CIFailureLogTail: &tail,
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}

	if len(msgr.sent) != 1 {
		t.Fatalf("want 1 send, got %d", len(msgr.sent))
	}
	if got := msgr.sent[0].Message; !strings.Contains(got, "CI is failing") || !strings.Contains(got, tail) {
		t.Errorf("message missing base text or log tail: %q", got)
	}
	if notifyCount(notf, "reaction.escalated") != 0 {
		t.Error("a first failure must not escalate")
	}
}

func TestReaction_ApprovedAndGreenNotifiesNeverAutoMerges(t *testing.T) {
	m, store, notf, msgr := newReactive()
	store.seed(sid, lcOpenPR(domain.PRReasonReviewPending))

	err := m.ApplySCMObservation(ctx(), sid, ports.SCMFacts{
		Fetched: true, PRState: domain.PROpen, ReviewDecision: ports.ReviewApproved,
		Mergeability: ports.Mergeability{Mergeable: true}, PRNumber: 7,
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}

	// approved-and-green is notify (human decides to merge); the agent is never
	// messaged and no auto-merge fires.
	if len(msgr.sent) != 0 {
		t.Errorf("approved-and-green must not message the agent, got %d sends", len(msgr.sent))
	}
	if notifyCount(notf, "reaction.approved-and-green") != 1 {
		t.Errorf("want one approved-and-green notify, got events %+v", notf.events)
	}
}

func TestReaction_NotifyEventsForHardStates(t *testing.T) {
	tests := []struct {
		name      string
		apply     func(m *Manager)
		eventType string
	}{
		{
			name:      "waiting_input -> agent-needs-input",
			apply:     func(m *Manager) { applyActivity(m, domain.ActivityWaitingInput) },
			eventType: "reaction.agent-needs-input",
		},
		{
			name:      "blocked -> agent-stuck",
			apply:     func(m *Manager) { applyActivity(m, domain.ActivityBlocked) },
			eventType: "reaction.agent-stuck",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, store, notf, msgr := newReactive()
			store.seed(sid, lc(domain.SessionWorking, domain.ReasonTaskInProgress, domain.RuntimeAlive))
			tc.apply(m)
			if notifyCount(notf, tc.eventType) != 1 {
				t.Errorf("want one %s, got events %+v", tc.eventType, notf.events)
			}
			if len(msgr.sent) != 0 {
				t.Errorf("notify reaction must not message the agent, got %d", len(msgr.sent))
			}
		})
	}
}

func TestReaction_InferredDeathNotifiesAgentExited(t *testing.T) {
	m, store, notf, _ := newReactive()
	store.seed(sid, detectingLC())

	err := m.ApplyRuntimeObservation(ctx(), sid, ports.RuntimeFacts{
		RuntimeState: ports.RuntimeProbeDead, ProcessState: ports.ProcessProbeDead, ObservedAt: t0,
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if l := mustLoad(t, store); domain.DeriveLegacyStatus(l) != domain.StatusKilled {
		t.Fatalf("precondition: want killed, got %s", domain.DeriveLegacyStatus(l))
	}
	if notifyCount(notf, "reaction.agent-exited") != 1 {
		t.Errorf("want one agent-exited, got events %+v", notf.events)
	}
}

func TestReaction_PRClosedAndMerged(t *testing.T) {
	tests := []struct {
		name      string
		prState   domain.PRState
		eventType string
	}{
		{"closed -> pr-closed", domain.PRClosed, "reaction.pr-closed"},
		{"merged -> all-complete", domain.PRMerged, "reaction.all-complete"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, store, notf, _ := newReactive()
			store.seed(sid, lcOpenPR(domain.PRReasonReviewPending))
			err := m.ApplySCMObservation(ctx(), sid, ports.SCMFacts{
				Fetched: true, PRState: tc.prState, PRNumber: 7,
			})
			if err != nil {
				t.Fatalf("apply: %v", err)
			}
			if notifyCount(notf, tc.eventType) != 1 {
				t.Errorf("want one %s, got events %+v", tc.eventType, notf.events)
			}
		})
	}
}

func TestReaction_OnKillRequestedDoesNotReact(t *testing.T) {
	m, store, notf, msgr := newReactive()
	store.seed(sid, lc(domain.SessionWorking, domain.ReasonTaskInProgress, domain.RuntimeAlive))

	if err := m.OnKillRequested(ctx(), sid, ports.KillReason{Kind: ports.KillManual}); err != nil {
		t.Fatalf("kill: %v", err)
	}
	// An explicit human kill is not an inferred event: no agent-exited, no send.
	if len(notf.events) != 0 || len(msgr.sent) != 0 {
		t.Errorf("explicit kill must fire no reaction: notifies=%+v sends=%+v", notf.events, msgr.sent)
	}
}

// ---- escalation engine ----

func TestReaction_CIFailedNumericEscalation(t *testing.T) {
	m, store, notf, msgr := newReactive()
	store.seed(sid, lcOpenPR(domain.PRReasonReviewPending))

	// ci-failed has retries 2 and is persistent, so the budget is shared across
	// fail->pending->fail oscillations and escalates on the third failure.
	failN := 4
	for i := 0; i < failN; i++ {
		failCI(t, m)
		pendingCI(t, m) // oscillate out (persistent tracker must NOT reset)
	}

	if len(msgr.sent) != 2 {
		t.Errorf("want 2 auto-sends before escalation, got %d", len(msgr.sent))
	}
	if c := notifyCount(notf, "reaction.escalated"); c != 1 {
		t.Errorf("want exactly one escalation, got %d", c)
	}
}

func TestReaction_DurationEscalationFiresOnTick(t *testing.T) {
	m, store, notf, msgr := newReactive()
	store.seed(sid, lcOpenPR(domain.PRReasonReviewPending))

	// changes-requested: send once now, then escalate by duration (30m) — which
	// only the reaper's TickEscalations can fire (the LCM never polls).
	err := m.ApplySCMObservation(ctx(), sid, ports.SCMFacts{
		Fetched: true, PRState: domain.PROpen, ReviewDecision: ports.ReviewChangesRequested, PRNumber: 7,
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(msgr.sent) != 1 {
		t.Fatalf("want one send on transition, got %d", len(msgr.sent))
	}

	if err := m.TickEscalations(ctx(), t0.Add(10*time.Minute)); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if notifyCount(notf, "reaction.escalated") != 0 {
		t.Error("must not escalate before escalateAfter elapses")
	}

	// Inclusive boundary: escalate at exactly escalateAfter (30m), not only past it.
	if err := m.TickEscalations(ctx(), t0.Add(30*time.Minute)); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if notifyCount(notf, "reaction.escalated") != 1 {
		t.Errorf("want one duration escalation at exactly 30m, got events %+v", notf.events)
	}
}

func TestReaction_KillClearsEscalationTrackers(t *testing.T) {
	m, store, notf, _ := newReactive()
	store.seed(sid, lcOpenPR(domain.PRReasonReviewPending))

	// changes-requested creates a duration-based tracker.
	if err := m.ApplySCMObservation(ctx(), sid, ports.SCMFacts{
		Fetched: true, PRState: domain.PROpen, ReviewDecision: ports.ReviewChangesRequested, PRNumber: 7,
	}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if sessionTrackerCount(m, sid) == 0 {
		t.Fatalf("precondition: expected a tracker")
	}

	if err := m.OnKillRequested(ctx(), sid, ports.KillReason{Kind: ports.KillManual}); err != nil {
		t.Fatalf("kill: %v", err)
	}
	if n := sessionTrackerCount(m, sid); n != 0 {
		t.Errorf("kill must clear trackers, %d left", n)
	}
	// A later duration tick must not escalate a dead session.
	if err := m.TickEscalations(ctx(), t0.Add(time.Hour)); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if c := notifyCount(notf, "reaction.escalated"); c != 0 {
		t.Errorf("killed session must not escalate, got %d", c)
	}
}

func TestReaction_SendFailureDoesNotBurnBudget(t *testing.T) {
	store := newFakeStore()
	notf := &recordingNotifier{}
	fm := &failingMessenger{}
	m := New(store, notf, fm)
	m.clock = func() time.Time { return t0 }
	store.seed(sid, lcOpenPR(domain.PRReasonReviewPending))

	tail := "fail"
	failing := ports.SCMFacts{Fetched: true, PRState: domain.PROpen, CISummary: ports.CIFailing, PRNumber: 7, CIFailureLogTail: &tail}
	pending := ports.SCMFacts{Fetched: true, PRState: domain.PROpen, CISummary: ports.CIPending, ReviewDecision: ports.ReviewPending, PRNumber: 7}

	// ci-failed has retries 2; with every delivery failing, the budget is rolled
	// back each time, so even 5 failures never escalate.
	for i := 0; i < 5; i++ {
		_ = m.ApplySCMObservation(ctx(), sid, failing) // returns the delivery error
		_ = m.ApplySCMObservation(ctx(), sid, pending)
	}
	if fm.attempts < 5 {
		t.Errorf("expected at least 5 send attempts, got %d", fm.attempts)
	}
	if c := notifyCount(notf, "reaction.escalated"); c != 0 {
		t.Errorf("undelivered messages must not escalate, got %d", c)
	}
}

func TestReaction_NonPersistentTrackerClearsOnLeave(t *testing.T) {
	m, store, _, msgr := newReactive()
	store.seed(sid, lc(domain.SessionWorking, domain.ReasonTaskInProgress, domain.RuntimeAlive))

	// agent-idle has retries 2 but is NOT persistent: leaving idle clears the
	// tracker, so three idle incidents each send fresh and none escalate.
	for i := 0; i < 3; i++ {
		applyActivity(m, domain.ActivityIdle)
		applyActivity(m, domain.ActivityActive)
	}
	if len(msgr.sent) != 3 {
		t.Errorf("want 3 idle sends (budget reset each incident), got %d", len(msgr.sent))
	}
}

func TestReaction_CIFailedRearmsOnGenuineRecovery(t *testing.T) {
	m, store, notf, msgr := newReactive()
	store.seed(sid, lcOpenPR(domain.PRReasonReviewPending))

	// Drain the ci-failed budget to escalation (silenced thereafter).
	for i := 0; i < 4; i++ {
		failCI(t, m)
		pendingCI(t, m)
	}
	if notifyCount(notf, "reaction.escalated") != 1 {
		t.Fatalf("precondition: want one escalation, got %d", notifyCount(notf, "reaction.escalated"))
	}
	sentBefore := len(msgr.sent)

	// A genuine recovery (approved + green) ends the incident and re-arms the
	// budget; a later regression must re-nudge the agent, not stay silenced.
	if err := m.ApplySCMObservation(ctx(), sid, ports.SCMFacts{
		Fetched: true, PRState: domain.PROpen, ReviewDecision: ports.ReviewApproved,
		Mergeability: ports.Mergeability{Mergeable: true}, PRNumber: 7,
	}); err != nil {
		t.Fatalf("recover: %v", err)
	}
	failCI(t, m)

	if len(msgr.sent) != sentBefore+1 {
		t.Errorf("regression after recovery must re-nudge the agent: sends %d -> %d", sentBefore, len(msgr.sent))
	}
}

func TestReaction_IncidentOverClearsAllSessionTrackers(t *testing.T) {
	m, store, _, _ := newReactive()
	store.seed(sid, lcOpenPR(domain.PRReasonReviewPending))

	failCI(t, m) // creates a persistent ci-failed tracker
	if sessionTrackerCount(m, sid) == 0 {
		t.Fatalf("precondition: expected a ci-failed tracker")
	}

	// Merging ends the incident; no tracker (and no stale escalated=true) may
	// survive for the session.
	if err := m.ApplySCMObservation(ctx(), sid, ports.SCMFacts{
		Fetched: true, PRState: domain.PRMerged, PRNumber: 7,
	}); err != nil {
		t.Fatalf("merge: %v", err)
	}
	if n := sessionTrackerCount(m, sid); n != 0 {
		t.Errorf("incident over must clear all trackers, %d left", n)
	}
}

func sessionTrackerCount(m *Manager, id domain.SessionID) int {
	m.trackerMu.Lock()
	defer m.trackerMu.Unlock()
	c := 0
	for k := range m.trackers {
		if k.id == id {
			c++
		}
	}
	return c
}

// ---- TickEscalations never writes canonical state ----

func TestTickEscalations_DoesNotPersist(t *testing.T) {
	m, store, _, _ := newReactive()
	store.seed(sid, lc(domain.SessionWorking, domain.ReasonTaskInProgress, domain.RuntimeAlive))
	if err := m.TickEscalations(ctx(), t0); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if l := mustLoad(t, store); l.Revision != 0 {
		t.Errorf("TickEscalations must not write canonical state, got revision=%d", l.Revision)
	}
}

// ---- helpers ----

func applyActivity(m *Manager, a domain.ActivityState) {
	_ = m.ApplyActivitySignal(ctx(), sid, ports.ActivitySignal{
		State: ports.SignalValid, Activity: a, Timestamp: t0, Source: domain.SourceHook,
	})
}

func failCI(t *testing.T, m *Manager) {
	t.Helper()
	tail := "fail"
	if err := m.ApplySCMObservation(ctx(), sid, ports.SCMFacts{
		Fetched: true, PRState: domain.PROpen, CISummary: ports.CIFailing, PRNumber: 7, CIFailureLogTail: &tail,
	}); err != nil {
		t.Fatalf("failCI: %v", err)
	}
}

func pendingCI(t *testing.T, m *Manager) {
	t.Helper()
	if err := m.ApplySCMObservation(ctx(), sid, ports.SCMFacts{
		Fetched: true, PRState: domain.PROpen, CISummary: ports.CIPending, ReviewDecision: ports.ReviewPending, PRNumber: 7,
	}); err != nil {
		t.Fatalf("pendingCI: %v", err)
	}
}
