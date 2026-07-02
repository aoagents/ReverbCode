package daemon

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/cdc"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// recordingSink captures every emitted event for assertions.
type recordingSink struct {
	mu     sync.Mutex
	events []ports.TelemetryEvent
}

func (s *recordingSink) Emit(_ context.Context, ev ports.TelemetryEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, ev)
}

func (s *recordingSink) Close(context.Context) error { return nil }

func (s *recordingSink) names() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.events))
	for i, ev := range s.events {
		out[i] = ev.Name
	}
	return out
}

func (s *recordingSink) count(name string) int {
	n := 0
	for _, got := range s.names() {
		if got == name {
			n++
		}
	}
	return n
}

func prEvent(t *testing.T, typ cdc.EventType, payload map[string]any) cdc.Event {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return cdc.Event{ProjectID: "proj-1", SessionID: "sess-1", Type: typ, Payload: raw}
}

func TestEmitPRRaised_FirstIsOncePerInstall(t *testing.T) {
	sink := &recordingSink{}
	ms := newMilestoneStore(t.TempDir())
	ev := prEvent(t, cdc.EventPRCreated, map[string]any{"url": "u1", "state": "open"})

	emitPRRaised(sink, ms, ev, nil)
	emitPRRaised(sink, ms, prEvent(t, cdc.EventPRCreated, map[string]any{"url": "u2", "state": "open"}), nil)

	if got := sink.count("ao.session.pr_raised"); got != 2 {
		t.Fatalf("pr_raised count = %d, want 2", got)
	}
	if got := sink.count("ao.onboarding.first_pr_raised"); got != 1 {
		t.Fatalf("first_pr_raised count = %d, want 1", got)
	}
}

func TestEmitPRMerged_OnlyOnMergedAndDeduped(t *testing.T) {
	sink := &recordingSink{}
	ms := newMilestoneStore(t.TempDir())

	// A non-merged update emits nothing.
	emitPRMerged(sink, ms, prEvent(t, cdc.EventPRUpdated, map[string]any{"url": "u1", "state": "open"}), nil)
	if len(sink.names()) != 0 {
		t.Fatalf("expected no events for non-merged update, got %v", sink.names())
	}

	merged := prEvent(t, cdc.EventPRUpdated, map[string]any{"url": "u1", "state": "merged"})
	emitPRMerged(sink, ms, merged, nil)
	// Repeated merged update for same PR must not re-emit.
	emitPRMerged(sink, ms, merged, nil)

	if got := sink.count("ao.session.pr_merged"); got != 1 {
		t.Fatalf("pr_merged count = %d, want 1", got)
	}
	if got := sink.count("ao.onboarding.first_pr_merged"); got != 1 {
		t.Fatalf("first_pr_merged count = %d, want 1", got)
	}
}

func TestEmitPRReviewed_DecisionGatedAndDeduped(t *testing.T) {
	sink := &recordingSink{}
	ms := newMilestoneStore(t.TempDir())

	// review "none" emits nothing.
	emitPRReviewed(sink, ms, prEvent(t, cdc.EventPRUpdated, map[string]any{"url": "u1", "review": "none"}), nil)
	if len(sink.names()) != 0 {
		t.Fatalf("expected no events for review=none, got %v", sink.names())
	}

	changes := prEvent(t, cdc.EventPRUpdated, map[string]any{"url": "u1", "review": "changes_requested"})
	emitPRReviewed(sink, ms, changes, nil)
	emitPRReviewed(sink, ms, changes, nil) // same decision -> deduped
	approved := prEvent(t, cdc.EventPRUpdated, map[string]any{"url": "u1", "review": "approved"})
	emitPRReviewed(sink, ms, approved, nil)

	if got := sink.count("ao.session.pr_reviewed"); got != 2 {
		t.Fatalf("pr_reviewed count = %d, want 2 (one per distinct decision)", got)
	}
	if got := sink.count("ao.onboarding.first_pr_reviewed"); got != 1 {
		t.Fatalf("first_pr_reviewed count = %d, want 1", got)
	}
}

func TestEmitPRRevised_PerThreadDeduped(t *testing.T) {
	sink := &recordingSink{}
	ms := newMilestoneStore(t.TempDir())

	t1 := prEvent(t, cdc.EventPRReviewThreadResolved, map[string]any{"pr": "u1", "thread": "t1", "resolved": true})
	emitPRRevised(sink, ms, t1)
	emitPRRevised(sink, ms, t1) // same thread -> deduped
	emitPRRevised(sink, ms, prEvent(t, cdc.EventPRReviewThreadResolved, map[string]any{"pr": "u1", "thread": "t2", "resolved": true}))

	if got := sink.count("ao.session.pr_revised"); got != 2 {
		t.Fatalf("pr_revised count = %d, want 2", got)
	}
	if got := sink.count("ao.onboarding.first_pr_revised"); got != 1 {
		t.Fatalf("first_pr_revised count = %d, want 1", got)
	}
}

func TestMilestoneStore_PersistsAcrossReload(t *testing.T) {
	dir := t.TempDir()
	ms := newMilestoneStore(dir)
	if !ms.claim("first_pr_raised") {
		t.Fatal("first claim should return true")
	}
	if ms.claim("first_pr_raised") {
		t.Fatal("second claim in same store should return false")
	}
	reloaded := newMilestoneStore(dir)
	if reloaded.claim("first_pr_raised") {
		t.Fatal("claim after reload should return false (persisted)")
	}
	if !reloaded.claimed("first_pr_raised") {
		t.Fatal("claimed() should report true after reload")
	}
}

func TestStartOnboardingCDC_RoutesThroughBroadcaster(t *testing.T) {
	sink := &recordingSink{}
	ms := newMilestoneStore(t.TempDir())
	bcast := cdc.NewBroadcaster()
	unsub := startOnboardingCDC(bcast, sink, ms, nil)
	defer unsub()

	bcast.Publish(prEvent(t, cdc.EventPRCreated, map[string]any{"url": "u1", "state": "open"}))
	bcast.Publish(prEvent(t, cdc.EventPRUpdated, map[string]any{"url": "u1", "state": "merged"}))

	if got := sink.count("ao.session.pr_raised"); got != 1 {
		t.Fatalf("pr_raised via broadcaster = %d, want 1", got)
	}
	if got := sink.count("ao.session.pr_merged"); got != 1 {
		t.Fatalf("pr_merged via broadcaster = %d, want 1", got)
	}
	if got := sink.count("ao.onboarding.first_pr_raised"); got != 1 {
		t.Fatalf("first_pr_raised via broadcaster = %d, want 1", got)
	}
}

func TestEmitPrereqsTelemetry_SkipsWhenAlreadyReady(t *testing.T) {
	sink := &recordingSink{}
	ms := newMilestoneStore(t.TempDir())
	emitPrereqsTelemetry(context.Background(), sink, ms, func() bool { return true })
	if len(sink.names()) != 0 {
		t.Fatalf("expected no events when already ready, got %v", sink.names())
	}
}

func TestEmitPrereqsTelemetry_EmitsChecked(t *testing.T) {
	sink := &recordingSink{}
	ms := newMilestoneStore(t.TempDir())
	emitPrereqsTelemetry(context.Background(), sink, ms, func() bool { return false })
	if got := sink.count("ao.onboarding.prereqs_checked"); got != 1 {
		t.Fatalf("prereqs_checked count = %d, want 1", got)
	}
}
