package terminal

import (
	"context"
	"encoding/base64"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/cdc"
)

// fakeConn is an in-memory wsConn driven by channels.
type fakeConn struct {
	in     chan clientMsg
	out    chan serverMsg
	pings  int32
	once   sync.Once
	closed chan struct{}
}

func newFakeConn() *fakeConn {
	return &fakeConn{in: make(chan clientMsg, 16), out: make(chan serverMsg, 64), closed: make(chan struct{})}
}

func (c *fakeConn) ReadJSON(ctx context.Context, v any) error {
	select {
	case m := <-c.in:
		*(v.(*clientMsg)) = m
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.closed:
		return context.Canceled
	}
}

func (c *fakeConn) WriteJSON(_ context.Context, v any) error {
	c.out <- v.(serverMsg)
	return nil
}

func (c *fakeConn) Ping(context.Context) error {
	atomic.AddInt32(&c.pings, 1)
	return nil
}

func (c *fakeConn) Close(string) error {
	c.once.Do(func() { close(c.closed) })
	return nil
}

// recv waits for a frame of the given channel+type, draining others.
func recv(t *testing.T, c *fakeConn, ch, typ string, d time.Duration) serverMsg {
	t.Helper()
	deadline := time.After(d)
	for {
		select {
		case m := <-c.out:
			if m.Ch == ch && m.Type == typ {
				return m
			}
		case <-deadline:
			t.Fatalf("did not receive %s/%s within %s", ch, typ, d)
		}
	}
}

func TestServeOpenStreamsAndWritesTerminal(t *testing.T) {
	src := &fakeSource{alive: true}
	pty := newFakePTY()
	sp := &fakeSpawner{ptys: []*fakePTY{pty}}
	mgr := NewManager(src, nil, testLogger(), WithSpawn(sp.spawn), WithHeartbeat(0))
	defer mgr.Close()

	conn := newFakeConn()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go mgr.Serve(ctx, conn)

	conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgOpen}
	recv(t, conn, chTerminal, msgOpened, time.Second)

	pty.push([]byte("prompt$ "))
	data := recv(t, conn, chTerminal, msgData, time.Second)
	got, _ := base64.StdEncoding.DecodeString(data.Data)
	if string(got) != "prompt$ " {
		t.Fatalf("streamed data = %q", got)
	}

	conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgData, Data: base64.StdEncoding.EncodeToString([]byte("whoami\n"))}
	eventually(t, time.Second, func() bool { return string(pty.writtenBytes()) == "whoami\n" })

	conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgResize, Rows: 30, Cols: 100}
	eventually(t, time.Second, func() bool {
		rs := pty.resizeCalls()
		return len(rs) == 1 && rs[0] == [2]uint16{30, 100}
	})
}

// nextTerminal returns the next frame on conn.out (no skipping), so callers can
// assert frame ordering rather than just presence.
func nextTerminal(t *testing.T, c *fakeConn) serverMsg {
	t.Helper()
	select {
	case m := <-c.out:
		return m
	case <-time.After(time.Second):
		t.Fatal("no frame within 1s")
		return serverMsg{}
	}
}

// Opening a pane whose runtime is already dead must (1) send opened before
// exited (the dead pane is reported, not errored) and (2) clear the conn's
// entry, so a later open for the same id on this connection is still served
// instead of being silently dropped by the already-open guard.
func TestServeOpenDeadRuntimeReportsExitedAndAllowsReopen(t *testing.T) {
	src := &fakeSource{alive: false}
	sp := &fakeSpawner{}
	mgr := NewManager(src, nil, testLogger(), WithSpawn(sp.spawn), WithHeartbeat(0))
	defer mgr.Close()

	conn := newFakeConn()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go mgr.Serve(ctx, conn)

	conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgOpen}
	if m := nextTerminal(t, conn); m.Type != msgOpened {
		t.Fatalf("first frame = %q, want opened", m.Type)
	}
	if m := nextTerminal(t, conn); m.Type != msgExited {
		t.Fatalf("second frame = %q, want exited", m.Type)
	}
	if got := sp.calls(); got != 0 {
		t.Fatalf("attach must never run against a dead runtime, got %d spawns", got)
	}

	conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgOpen}
	if m := nextTerminal(t, conn); m.Type != msgOpened {
		t.Fatalf("re-open frame = %q, want opened (open was dropped, entry stuck)", m.Type)
	}
}

// A session that exits after being opened must clear its connection entry on
// exit, so a later open for the same id is served rather than dropped by the
// already-open guard.
func TestServeExitAfterOpenClearsEntryAllowingReopen(t *testing.T) {
	src := &fakeSource{alive: true} // alive for the first attach
	p := newFakePTY()
	sp := &fakeSpawner{ptys: []*fakePTY{p}}
	mgr := NewManager(src, nil, testLogger(), WithSpawn(sp.spawn), WithHeartbeat(0))
	defer mgr.Close()

	conn := newFakeConn()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go mgr.Serve(ctx, conn)

	conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgOpen}
	recv(t, conn, chTerminal, msgOpened, time.Second)

	src.setAlive(false) // a dropped pty must not re-attach -> session exits
	p.Close()           // drop the pty; IsAlive false => session exits, no re-attach
	recv(t, conn, chTerminal, msgExited, time.Second)

	conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgOpen}
	recv(t, conn, chTerminal, msgOpened, 2*time.Second)
}

// An attachment that exits the moment it is opened (dead runtime) fires onExit
// from its run goroutine, racing the reopen that follows the exited frame. The
// identity-guarded delete in onExit must never evict a successor attachment
// registered under the same id: every reopen must be served (opened), never
// silently dropped by the already-open guard. Stressed across many iterations
// to shake the exit/reopen interleavings out.
func TestServeReopenAfterImmediateExitNeverStuck(t *testing.T) {
	for i := 0; i < 400; i++ {
		src := &fakeSource{}
		src.setAlive(false) // dead runtime -> the open exits without attaching
		sp := &fakeSpawner{}
		mgr := NewManager(src, nil, testLogger(), WithSpawn(sp.spawn), WithHeartbeat(0))

		conn := newFakeConn()
		ctx, cancel := context.WithCancel(context.Background())
		go mgr.Serve(ctx, conn)

		conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgOpen}

		recv(t, conn, chTerminal, msgOpened, time.Second)
		recv(t, conn, chTerminal, msgExited, time.Second)

		// The reopen must be served even while the first open's exit teardown is
		// still in flight.
		conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgOpen}
		recv(t, conn, chTerminal, msgOpened, time.Second)

		cancel()
		mgr.Close()
	}
}

func TestServeRejectsOpenWithoutID(t *testing.T) {
	mgr := NewManager(&fakeSource{}, nil, testLogger(), WithSpawn((&fakeSpawner{}).spawn), WithHeartbeat(0))
	defer mgr.Close()
	conn := newFakeConn()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go mgr.Serve(ctx, conn)

	conn.in <- clientMsg{Ch: chTerminal, Type: msgOpen}
	msg := recv(t, conn, chTerminal, msgError, time.Second)
	if msg.Error == "" {
		t.Fatal("expected an error message for open without id")
	}
}

func TestServeForwardsSessionChannelFromCDC(t *testing.T) {
	bc := cdc.NewBroadcaster()
	mgr := NewManager(&fakeSource{}, bc, testLogger(), WithSpawn((&fakeSpawner{}).spawn), WithHeartbeat(0))
	defer mgr.Close()

	conn := newFakeConn()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go mgr.Serve(ctx, conn)

	conn.in <- clientMsg{Ch: chSubscribe, Type: msgSubscribe}
	// Give the subscription time to register before publishing.
	eventually(t, time.Second, func() bool {
		bc.Publish(cdc.Event{Seq: 9, ProjectID: "p1", SessionID: "s1", Type: cdc.EventSessionUpdated})
		select {
		case m := <-conn.out:
			return m.Ch == chSessions && m.Session != nil && m.Session.Seq == 9
		default:
			return false
		}
	})
}

func TestServeSystemPingGetsPong(t *testing.T) {
	mgr := NewManager(&fakeSource{}, nil, testLogger(), WithSpawn((&fakeSpawner{}).spawn), WithHeartbeat(0))
	defer mgr.Close()
	conn := newFakeConn()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go mgr.Serve(ctx, conn)

	conn.in <- clientMsg{Ch: chSystem, Type: msgPing}
	recv(t, conn, chSystem, msgPong, time.Second)
}

func TestServeHeartbeatPings(t *testing.T) {
	mgr := NewManager(&fakeSource{}, nil, testLogger(), WithSpawn((&fakeSpawner{}).spawn), WithHeartbeat(10*time.Millisecond))
	defer mgr.Close()
	conn := newFakeConn()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go mgr.Serve(ctx, conn)

	eventually(t, time.Second, func() bool { return atomic.LoadInt32(&conn.pings) >= 2 })
}

func TestServeClosesConnOnReadEnd(t *testing.T) {
	mgr := NewManager(&fakeSource{}, nil, testLogger(), WithSpawn((&fakeSpawner{}).spawn), WithHeartbeat(0))
	defer mgr.Close()
	conn := newFakeConn()
	ctx, cancel := context.WithCancel(context.Background())
	go mgr.Serve(ctx, conn)

	cancel() // client/server context ends
	select {
	case <-conn.closed:
	case <-time.After(time.Second):
		t.Fatal("Serve must close the conn when the context is cancelled")
	}
}

// Each connection opening the same pane gets its OWN attach PTY — that is the
// per-client model: zellij replays its init handshake + full repaint to every
// fresh attach, so no client depends on bytes another client consumed. Output
// pushed to one client's PTY must reach only that client, and closing one
// client's terminal must not touch the other's PTY.
func TestServePerClientAttachIsolation(t *testing.T) {
	src := &fakeSource{alive: true}
	p1, p2 := newFakePTY(), newFakePTY()
	sp := &fakeSpawner{ptys: []*fakePTY{p1, p2}}
	mgr := NewManager(src, nil, testLogger(), WithSpawn(sp.spawn), WithHeartbeat(0))
	defer mgr.Close()

	connA, connB := newFakeConn(), newFakeConn()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go mgr.Serve(ctx, connA)
	go mgr.Serve(ctx, connB)

	connA.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgOpen}
	recv(t, connA, chTerminal, msgOpened, time.Second)
	eventually(t, time.Second, func() bool { return sp.calls() == 1 })

	connB.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgOpen}
	recv(t, connB, chTerminal, msgOpened, time.Second)
	eventually(t, time.Second, func() bool { return sp.calls() == 2 })

	// Zellij fans output out per attach; here each fake PTY stands in for one
	// attach process, so its bytes must reach exactly its own connection.
	p1.push([]byte("for-A"))
	data := recv(t, connA, chTerminal, msgData, time.Second)
	got, _ := base64.StdEncoding.DecodeString(data.Data)
	if string(got) != "for-A" {
		t.Fatalf("conn A data = %q", got)
	}
	select {
	case m := <-connB.out:
		t.Fatalf("conn B received %s/%s for conn A's PTY output", m.Ch, m.Type)
	default:
	}

	// Closing A's terminal detaches A only: B's PTY stays live.
	connA.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgClose}
	eventually(t, time.Second, func() bool {
		select {
		case <-p1.closed:
			return true
		default:
			return false
		}
	})
	select {
	case <-p2.closed:
		t.Fatal("closing conn A's terminal must not close conn B's PTY")
	default:
	}
}

// The open frame carries the client's grid; the PTY must start at that size
// rather than the kernel default, even though the attach is asynchronous.
func TestServeOpenAppliesInitialSize(t *testing.T) {
	src := &fakeSource{alive: true}
	pty := newFakePTY()
	sp := &fakeSpawner{ptys: []*fakePTY{pty}}
	mgr := NewManager(src, nil, testLogger(), WithSpawn(sp.spawn), WithHeartbeat(0))
	defer mgr.Close()

	conn := newFakeConn()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go mgr.Serve(ctx, conn)

	conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgOpen, Rows: 40, Cols: 120}
	recv(t, conn, chTerminal, msgOpened, time.Second)
	eventually(t, time.Second, func() bool {
		rs := pty.resizeCalls()
		return len(rs) == 1 && rs[0] == [2]uint16{40, 120}
	})
}

// Manager.Close must kill every live attach PTY: a PTY left open keeps its
// attach process running and deadlocks daemon shutdown.
func TestManagerCloseKillsLiveAttachments(t *testing.T) {
	src := &fakeSource{alive: true}
	pty := newFakePTY()
	sp := &fakeSpawner{ptys: []*fakePTY{pty}}
	mgr := NewManager(src, nil, testLogger(), WithSpawn(sp.spawn), WithHeartbeat(0))

	conn := newFakeConn()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go mgr.Serve(ctx, conn)

	conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgOpen}
	recv(t, conn, chTerminal, msgOpened, time.Second)
	eventually(t, time.Second, func() bool { return sp.calls() == 1 })

	mgr.Close()
	select {
	case <-pty.closed:
	case <-time.After(time.Second):
		t.Fatal("Manager.Close must close live attach PTYs")
	}
}

func TestEnqueueOverflowCancelsConn(t *testing.T) {
	cancelled := make(chan struct{})
	c := &connState{
		out:    make(chan serverMsg, 1),
		cancel: func() { close(cancelled) },
		terms:  map[string]*attachment{},
	}
	c.enqueue(serverMsg{Ch: chTerminal, Type: msgData}) // fills buffer
	c.enqueue(serverMsg{Ch: chTerminal, Type: msgData}) // overflow -> cancel
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("overflow must cancel the connection")
	}
}
