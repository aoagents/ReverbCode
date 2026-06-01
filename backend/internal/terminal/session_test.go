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
	src := &fakeSource{}
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
	src := &fakeSource{}
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
	src := &fakeSource{}
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
	src := &fakeSource{alive: false} // Zellij session gone -> no re-attach
	pty := newFakePTY()
	sp := &fakeSpawner{ptys: []*fakePTY{pty}}
	s := newTestSession(src, sp.spawn)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	exited := make(chan struct{})
	go s.run(ctx)
	s.subscribe(func([]byte) {}, func() { close(exited) })

	pty.Close() // pane ends
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("expected exit callback after clean pane exit")
	}
	if got := sp.calls(); got != 1 {
		t.Fatalf("expected exactly one attach, got %d", got)
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
	src := &fakeSource{attachErr: io.ErrUnexpectedEOF}
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
