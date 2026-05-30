package cdc

import "testing"

// TestBroadcastPublishSurvivesPanickingSubscriber verifies that a panicking
// subscriber neither propagates the panic out of Publish nor prevents other
// subscribers from receiving the event. The fix copies the subs slice under
// RLock and wraps each callback in a recover.
func TestBroadcastPublishSurvivesPanickingSubscriber(t *testing.T) {
	b := NewBroadcaster()
	b.Subscribe(func(e Event) {
		panic("subscriber misbehaved")
	})

	var got []Event
	b.Subscribe(func(e Event) {
		got = append(got, e)
	})

	// Must not panic; must still deliver to the recording subscriber.
	b.Publish(Event{Seq: 1, SessionID: "s1"})

	if len(got) != 1 {
		t.Fatalf("recording subscriber got %d events, want 1", len(got))
	}
	if got[0].Seq != 1 || got[0].SessionID != "s1" {
		t.Fatalf("unexpected event delivered: %+v", got[0])
	}
}

// TestBroadcastReentrantSubscribeDoesNotDeadlock verifies that a subscriber may
// call Subscribe / Unsubscribe from inside its own callback. The old
// implementation iterated subs under RLock and would self-deadlock on the write
// side when re-entered.
func TestBroadcastReentrantSubscribeDoesNotDeadlock(t *testing.T) {
	b := NewBroadcaster()
	done := make(chan struct{})

	b.Subscribe(func(e Event) {
		// Re-entrantly subscribe + immediately unsubscribe from inside the
		// callback. With the lock held across iteration this would deadlock.
		unsub := b.Subscribe(func(Event) {})
		unsub()
		close(done)
	})

	b.Publish(Event{Seq: 1})

	select {
	case <-done:
	default:
		t.Fatal("re-entrant subscribe did not complete (likely deadlock)")
	}
}
