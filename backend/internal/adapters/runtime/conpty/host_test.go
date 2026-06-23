package conpty

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// fakePTY implements ptyConn using in-memory pipes. Used only in tests;
// the real ConPTY impl is Windows-only.
// ---------------------------------------------------------------------------

type fakePTY struct {
	// output is what the fake "terminal" writes to the host (PTY -> host reader)
	outR *io.PipeReader
	outW *io.PipeWriter

	// input is what the host writes to the fake terminal (keystrokes)
	inR *io.PipeReader
	inW *io.PipeWriter

	resizeMu sync.Mutex
	resizes  []ResizePayload

	doneOnce sync.Once
	doneC    chan struct{}
	exitCode int
	closed   bool
	closeMu  sync.Mutex

	pid int
}

func newFakePTY(pid int) *fakePTY {
	outR, outW := io.Pipe()
	inR, inW := io.Pipe()
	return &fakePTY{
		outR:  outR,
		outW:  outW,
		inR:   inR,
		inW:   inW,
		doneC: make(chan struct{}),
		pid:   pid,
	}
}

// WriteOutput simulates the PTY producing output (e.g. shell printing text).
func (f *fakePTY) WriteOutput(data []byte) (int, error) { return f.outW.Write(data) }

// CloseOutput simulates the PTY process exiting (closes the read side).
func (f *fakePTY) CloseOutput(code int) {
	f.exitCode = code
	f.outW.Close()
}

// ReadInput lets tests inspect what the host forwarded to the PTY.
func (f *fakePTY) ReadInput(buf []byte) (int, error) { return f.inR.Read(buf) }

// ptyConn interface implementation.
func (f *fakePTY) Read(b []byte) (int, error)  { return f.outR.Read(b) }
func (f *fakePTY) Write(b []byte) (int, error) { return f.inW.Write(b) }

func (f *fakePTY) Resize(cols, rows int) error {
	f.resizeMu.Lock()
	defer f.resizeMu.Unlock()
	f.resizes = append(f.resizes, ResizePayload{Cols: cols, Rows: rows})
	return nil
}

func (f *fakePTY) Close() error {
	f.closeMu.Lock()
	defer f.closeMu.Unlock()
	f.closed = true
	// Close both pipes so pumpPTY and any Read calls unblock.
	_ = f.outW.Close()
	_ = f.inW.Close()
	f.doneOnce.Do(func() { close(f.doneC) })
	return nil
}

func (f *fakePTY) Done() <-chan struct{} { return f.doneC }

func (f *fakePTY) ExitCode() (int, bool) {
	select {
	case <-f.doneC:
		return f.exitCode, true
	default:
		return 0, false
	}
}

func (f *fakePTY) PID() int { return f.pid }

// signalExit simulates the child process exiting, triggering the Done channel
// and ExitCode returning true.
func (f *fakePTY) signalExit(code int) {
	f.exitCode = code
	f.doneOnce.Do(func() { close(f.doneC) })
	_ = f.outW.Close() // unblocks pumpPTY's Read
}

// ---------------------------------------------------------------------------
// testClient wraps a net.Conn and a MessageParser for easy frame reading.
// ---------------------------------------------------------------------------

type testClient struct {
	conn    net.Conn
	frameC  chan struct{ typ byte; payload []byte }
	parser  *MessageParser
}

func newTestClient(t *testing.T, addr string) *testClient {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	tc := &testClient{
		conn:   conn,
		frameC: make(chan struct{ typ byte; payload []byte }, 64),
	}
	tc.parser = NewMessageParser(func(msgType byte, payload []byte) {
		tc.frameC <- struct{ typ byte; payload []byte }{msgType, payload}
	})
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				tc.parser.Feed(buf[:n])
			}
			if err != nil {
				close(tc.frameC)
				return
			}
		}
	}()
	return tc
}

// readFrame blocks until a frame arrives or 2s times out.
func (tc *testClient) readFrame(t *testing.T) (typ byte, payload []byte) {
	t.Helper()
	select {
	case f, ok := <-tc.frameC:
		if !ok {
			t.Fatal("client frame channel closed (connection dropped)")
		}
		return f.typ, f.payload
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for frame")
		return 0, nil
	}
}

// send writes a framed message to the server.
func (tc *testClient) send(msgType byte, payload []byte) error {
	_, err := tc.conn.Write(EncodeMessage(msgType, payload))
	return err
}

func (tc *testClient) close() { _ = tc.conn.Close() }

// ---------------------------------------------------------------------------
// Helper: start a Serve with a freshly created listener + fakePTY.
// ---------------------------------------------------------------------------

type serveFixture struct {
	pty    *fakePTY
	ring   *Ring
	ln     net.Listener
	addr   string
	cancel context.CancelFunc
	done   chan error
}

func startServe(t *testing.T, pid int) *serveFixture {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	pty := newFakePTY(pid)
	ring := NewRing()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, ServeConfig{
			SessionID: fmt.Sprintf("test-%d", pid),
			Listener:  ln,
			PTY:       pty,
			Ring:      ring,
		})
	}()
	return &serveFixture{
		pty:    pty,
		ring:   ring,
		ln:     ln,
		addr:   ln.Addr().String(),
		cancel: cancel,
		done:   done,
	}
}

// waitDone waits for Serve to return (up to 2s).
func (f *serveFixture) waitDone(t *testing.T) {
	t.Helper()
	select {
	case <-f.done:
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return in time")
	}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestScrollbackReplay: seed the ring, connect a client; first frame must be
// MsgTerminalData containing the ring snapshot.
func TestScrollbackReplay(t *testing.T) {
	f := startServe(t, 100)
	defer f.cancel()

	// Seed ring directly before the client connects.
	f.ring.Append([]byte("line1\nline2\n"))
	snap := f.ring.Snapshot()

	c := newTestClient(t, f.addr)
	defer c.close()

	typ, payload := c.readFrame(t)
	if typ != MsgTerminalData {
		t.Fatalf("got type 0x%02x, want MsgTerminalData", typ)
	}
	if string(payload) != string(snap) {
		t.Fatalf("scrollback payload = %q, want %q", payload, snap)
	}
}

// TestFanOut: two clients receive the same PTY output.
func TestFanOut(t *testing.T) {
	f := startServe(t, 101)
	defer f.cancel()

	c1 := newTestClient(t, f.addr)
	defer c1.close()
	c2 := newTestClient(t, f.addr)
	defer c2.close()

	// Write PTY output after both clients have connected.
	// We need to give the server a moment to register both clients; use a
	// brief sync by sending a status req from each and waiting for responses.
	// ponytail: channel-based sync via status round-trip avoids sleeps.
	_ = c1.send(MsgStatusReq, nil)
	_ = c2.send(MsgStatusReq, nil)
	// Drain status responses.
	c1.readFrame(t)
	c2.readFrame(t)

	msg := []byte("hello from pty\n")
	if _, err := f.pty.WriteOutput(msg); err != nil {
		t.Fatalf("WriteOutput: %v", err)
	}

	// Both clients should receive a MsgTerminalData with msg.
	for _, c := range []*testClient{c1, c2} {
		typ, payload := c.readFrame(t)
		if typ != MsgTerminalData {
			t.Fatalf("got type 0x%02x, want MsgTerminalData", typ)
		}
		if string(payload) != string(msg) {
			t.Fatalf("payload = %q, want %q", payload, msg)
		}
	}
}

// TestTerminalInput: MsgTerminalInput from a client reaches the fakePTY's input.
func TestTerminalInput(t *testing.T) {
	f := startServe(t, 102)
	defer f.cancel()

	c := newTestClient(t, f.addr)
	defer c.close()

	keystrokes := []byte("ls -la\r")
	if err := c.send(MsgTerminalInput, keystrokes); err != nil {
		t.Fatalf("send: %v", err)
	}

	buf := make([]byte, len(keystrokes))
	if _, err := io.ReadFull(f.pty.inR, buf); err != nil {
		t.Fatalf("read from pty input: %v", err)
	}
	if string(buf) != string(keystrokes) {
		t.Fatalf("pty input = %q, want %q", buf, keystrokes)
	}
}

// TestResize: MsgResize calls fakePTY.Resize with the right cols/rows.
func TestResize(t *testing.T) {
	f := startServe(t, 103)
	defer f.cancel()

	c := newTestClient(t, f.addr)
	defer c.close()

	payload, _ := json.Marshal(ResizePayload{Cols: 132, Rows: 40})
	if err := c.send(MsgResize, payload); err != nil {
		t.Fatalf("send: %v", err)
	}

	// Poll for the resize to arrive (it's async). Channel-based: send a
	// status req and wait for its reply, which guarantees the resize was
	// processed (single goroutine handles all messages per connection).
	_ = c.send(MsgStatusReq, nil)
	c.readFrame(t) // discard status response

	f.pty.resizeMu.Lock()
	resizes := f.pty.resizes
	f.pty.resizeMu.Unlock()

	if len(resizes) != 1 {
		t.Fatalf("got %d resize calls, want 1", len(resizes))
	}
	if resizes[0].Cols != 132 || resizes[0].Rows != 40 {
		t.Fatalf("resize = %+v, want {132 40}", resizes[0])
	}
}

// TestGetOutputReq: MsgGetOutputReq returns MsgGetOutputRes with ring.Tail(n).
func TestGetOutputReq(t *testing.T) {
	f := startServe(t, 104)
	defer f.cancel()

	f.ring.Append([]byte("alpha\nbeta\ngamma\n"))

	c := newTestClient(t, f.addr)
	defer c.close()

	// Drain scrollback frame.
	c.readFrame(t)

	reqPayload, _ := json.Marshal(GetOutputReq{Lines: 2})
	if err := c.send(MsgGetOutputReq, reqPayload); err != nil {
		t.Fatalf("send: %v", err)
	}

	typ, payload := c.readFrame(t)
	if typ != MsgGetOutputRes {
		t.Fatalf("got type 0x%02x, want MsgGetOutputRes", typ)
	}
	want := f.ring.Tail(2)
	if string(payload) != want {
		t.Fatalf("GetOutputRes = %q, want %q", payload, want)
	}
}

// TestStatusReq_AliveAndExited: MsgStatusReq returns alive:true while running;
// after the PTY exits, returns alive:false with exitCode. Listener stays open.
func TestStatusReq_AliveAndExited(t *testing.T) {
	f := startServe(t, 105)
	defer f.cancel()

	c := newTestClient(t, f.addr)
	defer c.close()

	// While running: expect alive:true.
	if err := c.send(MsgStatusReq, nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	typ, payload := c.readFrame(t)
	if typ != MsgStatusRes {
		t.Fatalf("got type 0x%02x, want MsgStatusRes", typ)
	}
	var sp StatusPayload
	if err := json.Unmarshal(payload, &sp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !sp.Alive {
		t.Fatalf("expected alive=true, got false")
	}
	if sp.PID != 105 {
		t.Fatalf("expected pid=105, got %d", sp.PID)
	}

	// Simulate PTY exit.
	f.pty.signalExit(42)

	// Drain the broadcast status-res that pumpPTY sends on exit.
	exitBcast, _ := c.readFrame(t)
	if exitBcast != MsgStatusRes {
		t.Fatalf("exit broadcast type = 0x%02x, want MsgStatusRes", exitBcast)
	}

	// Now a new status req should report alive:false.
	if err := c.send(MsgStatusReq, nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	typ2, payload2 := c.readFrame(t)
	if typ2 != MsgStatusRes {
		t.Fatalf("got type 0x%02x, want MsgStatusRes", typ2)
	}
	var sp2 StatusPayload
	if err := json.Unmarshal(payload2, &sp2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if sp2.Alive {
		t.Fatalf("expected alive=false after exit")
	}
	if sp2.ExitCode == nil || *sp2.ExitCode != 42 {
		t.Fatalf("expected exitCode=42, got %v", sp2.ExitCode)
	}

	// Keep-alive: the listener must still accept new connections.
	c2 := newTestClient(t, f.addr)
	defer c2.close()
	if err := c2.send(MsgStatusReq, nil); err != nil {
		t.Fatalf("keep-alive send: %v", err)
	}
	_, _ = c2.readFrame(t) // just verify it didn't crash
}

// TestKillReq: MsgKillReq disposes the fakePTY, drops clients, closes
// listener, and Serve returns.
func TestKillReq(t *testing.T) {
	f := startServe(t, 106)

	c := newTestClient(t, f.addr)

	if err := c.send(MsgKillReq, nil); err != nil {
		t.Fatalf("send: %v", err)
	}

	// Serve should return within 2s (includes the 50ms grace sleep).
	f.waitDone(t)

	// PTY Close must have been called.
	f.pty.closeMu.Lock()
	closed := f.pty.closed
	f.pty.closeMu.Unlock()
	if !closed {
		t.Fatal("expected pty.Close() to be called on kill")
	}

	// Listener should be closed: new dial must fail.
	conn, err := net.DialTimeout("tcp", f.addr, 200*time.Millisecond)
	if err == nil {
		_ = conn.Close()
		t.Fatal("expected listener to be closed after kill, but Dial succeeded")
	}

	c.close()
}

// TestShutdownViaCtxCancel: cancelling the context triggers graceful shutdown.
func TestShutdownViaCtxCancel(t *testing.T) {
	f := startServe(t, 107)

	c := newTestClient(t, f.addr)
	defer c.close()

	// Cancel the context.
	f.cancel()

	f.waitDone(t)

	// PTY Close must have been called.
	f.pty.closeMu.Lock()
	closed := f.pty.closed
	f.pty.closeMu.Unlock()
	if !closed {
		t.Fatal("expected pty.Close() on ctx cancel")
	}
}
