package terminal

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func newTestSession(src PTYSource, spawn spawnFunc) *session {
	return newSession("t1", ports.RuntimeHandle{ID: "t1"}, src, spawn, testLogger())
}

func TestSessionFansOutLiveOutputToSubscribers(t *testing.T) {
	src := &fakeSource{alive: true}
	pty := newFakePTY()
	sp := &fakeSpawner{ptys: []*fakePTY{pty}}
	s := newTestSession(src, sp.spawn)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.run(ctx)

	var a, b safeBytes
	s.subscribe(a.add, nil)
	s.subscribe(b.add, nil)

	pty.push([]byte("hello"))
	eventually(t, time.Second, func() bool { return a.string() == "hello" && b.string() == "hello" })
}

func TestSessionReplaysRingBufferOnSubscribe(t *testing.T) {
	src := &fakeSource{alive: true}
	pty := newFakePTY()
	sp := &fakeSpawner{ptys: []*fakePTY{pty}}
	s := newTestSession(src, sp.spawn)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.run(ctx)

	pty.push([]byte("scrollback"))
	eventually(t, time.Second, func() bool { return ringLen(s) == len("scrollback") })

	var late safeBytes
	s.subscribe(late.add, nil)
	eventually(t, time.Second, func() bool { return late.string() == "scrollback" })
}

func ringLen(s *session) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.ring.snapshot())
}

func TestSessionWriteAndResizeReachPTY(t *testing.T) {
	src := &fakeSource{alive: true}
	pty := newFakePTY()
	sp := &fakeSpawner{ptys: []*fakePTY{pty}}
	s := newTestSession(src, sp.spawn)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.run(ctx)

	eventually(t, time.Second, func() bool { return s.write([]byte("ls\n")) == nil })
	eventually(t, time.Second, func() bool { return string(pty.writtenBytes()) == "ls\n" })

	if err := s.resize(24, 80); err != nil {
		t.Fatalf("resize: %v", err)
	}
	eventually(t, time.Second, func() bool {
		rs := pty.resizeCalls()
		return len(rs) == 1 && rs[0] == [2]uint16{24, 80}
	})
}

func TestSessionSkipsReattachOnCleanExit(t *testing.T) {
	src := &fakeSource{alive: true} // alive for the first attach
	pty := newFakePTY()
	sp := &fakeSpawner{ptys: []*fakePTY{pty}}
	s := newTestSession(src, sp.spawn)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	exited := make(chan struct{})
	go s.run(ctx)
	s.subscribe(func([]byte) {}, func() { close(exited) })

	eventually(t, time.Second, func() bool { return sp.calls() == 1 })
	src.setAlive(false) // Zellij session gone -> no re-attach
	pty.Close()         // pane ends
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("expected exit callback after clean pane exit")
	}
	if got := sp.calls(); got != 1 {
		t.Fatalf("expected exactly one attach, got %d", got)
	}
}

// TestSessionNeverAttachesToDeadRuntime covers the resurrection bug: `zellij
// attach` on a killed-but-cached session resurrects it, re-running the agent
// command. A session whose runtime probes definitively dead must therefore
// report exited WITHOUT ever spawning an attach PTY — even on the very first
// open (the original code only checked liveness on re-attach).
func TestSessionNeverAttachesToDeadRuntime(t *testing.T) {
	src := &fakeSource{alive: false}
	sp := &fakeSpawner{}
	s := newTestSession(src, sp.spawn)

	exited := make(chan struct{})
	s.subscribe(func([]byte) {}, func() { close(exited) })

	go s.run(context.Background())
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("expected exit when runtime is dead before first attach")
	}
	if got := sp.calls(); got != 0 {
		t.Fatalf("attach must never run against a dead runtime, got %d spawns", got)
	}
}

// TestSessionRetriesProbeErrorsBeforeAttaching pins the hard rule that a
// failed liveness probe is NOT proof of death: a transient probe error must
// not flip the terminal to exited, and the attach proceeds once the probe
// recovers.
func TestSessionRetriesProbeErrorsBeforeAttaching(t *testing.T) {
	src := &fakeSource{aliveErr: io.ErrUnexpectedEOF}
	pty := newFakePTY()
	sp := &fakeSpawner{ptys: []*fakePTY{pty}}
	s := newTestSession(src, sp.spawn)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.run(ctx)

	// While probes error the session must neither exit nor attach.
	time.Sleep(50 * time.Millisecond)
	if s.isExited() {
		t.Fatal("probe error must not be treated as runtime death")
	}
	if got := sp.calls(); got != 0 {
		t.Fatalf("attach must wait for a successful probe, got %d spawns", got)
	}

	// Probe recovers -> the attach goes through.
	src.setAliveResult(true, nil)
	eventually(t, 2*time.Second, func() bool { return sp.calls() == 1 })
	if s.isExited() {
		t.Fatal("session exited despite a live runtime")
	}
}

func TestSessionReattachesWhileSessionAlive(t *testing.T) {
	src := &fakeSource{alive: true} // session still alive -> re-attach on drop
	p1, p2 := newFakePTY(), newFakePTY()
	sp := &fakeSpawner{ptys: []*fakePTY{p1, p2}}
	s := newTestSession(src, sp.spawn)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.run(ctx)

	eventually(t, time.Second, func() bool { return sp.calls() >= 1 })
	p1.Close() // first attach drops
	eventually(t, 2*time.Second, func() bool { return sp.calls() >= 2 })

	// Now the session is gone: the next drop must not re-attach.
	src.setAlive(false)
	p2.Close()
	eventually(t, 2*time.Second, func() bool { return s.isExited() })
}

func TestSessionFailsWhenAttachCommandErrors(t *testing.T) {
	src := &fakeSource{alive: true, attachErr: io.ErrUnexpectedEOF}
	sp := &fakeSpawner{}
	s := newTestSession(src, sp.spawn)

	exited := make(chan struct{})
	s.subscribe(func([]byte) {}, func() { close(exited) })

	go s.run(context.Background())
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("expected exit when attach command fails")
	}
	if sp.calls() != 0 {
		t.Fatalf("spawn should not run when attach command errors, got %d calls", sp.calls())
	}
}
