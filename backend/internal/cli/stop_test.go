package cli

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/runfile"
)

// TestWaitForStoppedKeepsRunFileFromConcurrentStart guards against deleting a
// fresh daemon's handshake: if a concurrent `ao start` replaces running.json
// with a new live PID while we are polling the PID we stopped, waitForStopped
// must report stopped but leave the new run-file intact.
func TestWaitForStoppedKeepsRunFileFromConcurrentStart(t *testing.T) {
	dir := t.TempDir()
	runFile := filepath.Join(dir, "running.json")

	const stoppedPID, newPID = 1111, 2222
	// running.json now belongs to a different, live daemon.
	if err := runfile.Write(runFile, runfile.Info{PID: newPID, Port: 3001, StartedAt: time.Unix(100, 0).UTC()}); err != nil {
		t.Fatal(err)
	}

	c := &commandContext{deps: Deps{
		ProcessAlive: func(pid int) bool { return pid == newPID }, // stoppedPID is dead
		Now:          func() time.Time { return time.Unix(200, 0).UTC() },
		Sleep:        func(time.Duration) {},
	}.withDefaults()}

	st, err := c.waitForStopped(context.Background(), stoppedPID, runFile, dir, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if st.State != stateStopped {
		t.Fatalf("state = %q, want stopped", st.State)
	}

	info, err := runfile.Read(runFile)
	if err != nil {
		t.Fatal(err)
	}
	if info == nil {
		t.Fatal("new daemon's run-file was deleted by stop of a different PID")
	}
	if info.PID != newPID {
		t.Fatalf("run-file PID = %d, want %d (new daemon)", info.PID, newPID)
	}
}

// TestWaitForStoppedRemovesOwnRunFile confirms the normal path still cleans up:
// when the dead PID owns the run-file, it is removed.
func TestWaitForStoppedRemovesOwnRunFile(t *testing.T) {
	dir := t.TempDir()
	runFile := filepath.Join(dir, "running.json")

	const stoppedPID = 1111
	if err := runfile.Write(runFile, runfile.Info{PID: stoppedPID, Port: 3001, StartedAt: time.Unix(100, 0).UTC()}); err != nil {
		t.Fatal(err)
	}

	c := &commandContext{deps: Deps{
		ProcessAlive: func(int) bool { return false },
		Now:          func() time.Time { return time.Unix(200, 0).UTC() },
		Sleep:        func(time.Duration) {},
	}.withDefaults()}

	st, err := c.waitForStopped(context.Background(), stoppedPID, runFile, dir, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if st.State != stateStopped {
		t.Fatalf("state = %q, want stopped", st.State)
	}
	info, err := runfile.Read(runFile)
	if err != nil {
		t.Fatal(err)
	}
	if info != nil {
		t.Fatalf("own run-file should have been removed, got %#v", info)
	}
}
