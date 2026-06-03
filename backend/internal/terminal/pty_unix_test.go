//go:build !windows

package terminal

import (
	"context"
	"testing"
	"time"
)

// TestCreackPTYCloseIsIdempotent guards the shutdown deadlock: the session run
// loop and session.close both call Close on the same PTY, so cmd.Wait must run
// exactly once. Without the sync.Once a second Wait blocks forever, so this test
// would hang (caught by the watchdog) rather than fail.
func TestCreackPTYCloseIsIdempotent(t *testing.T) {
	p, err := defaultSpawn(context.Background(), []string{"/bin/sh", "-c", "sleep 30"})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}

	done := make(chan struct{})
	go func() {
		_ = p.Close()
		_ = p.Close() // second close must not block on a second cmd.Wait
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("creackPTY.Close did not return: double Close deadlocked on cmd.Wait")
	}
}
