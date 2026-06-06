package crush

import (
	"testing"
)

func TestDeriveActivityStateReturnsFalse(t *testing.T) {
	state, ok := DeriveActivityState("some-event", []byte("payload"))
	if ok {
		t.Fatalf("unexpected ok: got true, want false (DeriveActivityState is a no-op for Crush)")
	}
	if state != "" {
		t.Fatalf("unexpected non-empty state: got %q", state)
	}
}
