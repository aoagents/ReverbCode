// Package conpty - host.go implements the serve engine for the pty-host
// detached process. It owns the agent's PTY (via the ptyConn seam), exposes
// it over a loopback TCP socket using the B1 binary protocol, replays
// scrollback to new clients, fans output to all connected clients, and shuts
// down gracefully (ConPTY dispose first, then clients, then listener).
//
// This file is cross-platform; only the real conptyConn impl is Windows-tagged.
package conpty

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"sync"
	"time"
)

// ptyConn is the host's handle to the running agent's pseudo-terminal.
// The real impl (conptyConn) lives in host_conpty_windows.go; tests use a fake.
type ptyConn interface {
	io.Reader // PTY output (raw bytes from the terminal)
	io.Writer // PTY input (keystrokes to the terminal)
	Resize(cols, rows int) error
	Close() error          // dispose the ConPTY
	Done() <-chan struct{} // closed when the child process exits
	ExitCode() (int, bool) // (code, true) once exited; (0, false) while running
	PID() int
}

// ServeConfig carries everything the host needs.
type ServeConfig struct {
	SessionID string
	Listener  net.Listener // caller provides (loopback); engine owns Accept loop
	PTY       ptyConn
	Ring      *Ring
}

// Serve runs the host event loop until the listener closes or Shutdown is
// invoked via the returned ShutdownFunc. It pumps PTY output into the ring
// and broadcasts to all clients, accepts new clients (replaying ring snapshot),
// and dispatches client messages. On PTY exit it broadcasts a status update
// but stays alive (keep-alive, mirroring tmux behavior). Returns when shut down.
func Serve(ctx context.Context, cfg ServeConfig) error {
	h := &host{
		cfg:       cfg,
		clients:   make(map[net.Conn]struct{}),
		shutdownC: make(chan struct{}),
	}
	return h.run(ctx)
}

// host holds the mutable state for a single pty-host session.
type host struct {
	cfg     ServeConfig
	mu      sync.Mutex
	clients map[net.Conn]struct{}

	shutdownOnce sync.Once
	shutdownC    chan struct{} // closed when Shutdown is called
}

// run is the main event loop.
func (h *host) run(ctx context.Context) error {
	// Pump PTY output to ring + broadcast.
	go h.pumpPTY()

	// Watch for ctx cancellation and trigger shutdown.
	go func() {
		select {
		case <-ctx.Done():
			h.shutdown()
		case <-h.shutdownC:
		}
	}()

	// runAcceptLoop accepts connections until the listener closes. A listener
	// close is normal (shutdown or external) and is treated as success.
	h.runAcceptLoop()
	return nil
}

// runAcceptLoop runs the Accept loop until the listener closes or returns an
// error. Listener-close errors are swallowed; they signal normal shutdown.
func (h *host) runAcceptLoop() {
	for {
		conn, err := h.cfg.Listener.Accept()
		if err != nil {
			return
		}
		go h.handleConn(conn)
	}
}

// shutdown is idempotent: disposes the ConPTY, closes clients, closes the
// listener. Mirrors the pty-host.ts shutdown() function.
// ponytail: 50ms sleep after pty.Close() gives the OS ConPTY helper
// (conpty_console_list_agent.exe) time to release cleanly; avoids the
// 0x800700e8 error dialog on Windows.
func (h *host) shutdown() {
	h.shutdownOnce.Do(func() {
		close(h.shutdownC)

		// 1. Dispose the ConPTY first (critical ordering).
		_ = h.cfg.PTY.Close()

		// 2. Brief grace so the OS ConPTY helper can clean up.
		time.Sleep(50 * time.Millisecond)

		// 3. Close all client connections.
		h.mu.Lock()
		for c := range h.clients {
			_ = c.Close()
		}
		h.clients = make(map[net.Conn]struct{})
		h.mu.Unlock()

		// 4. Close the listener to unblock Accept.
		_ = h.cfg.Listener.Close()
	})
}

// pumpPTY reads PTY output continuously, appends to the ring, and broadcasts
// to clients. On PTY exit it flushes the partial line and sends a status
// update but does NOT close the listener (keep-alive).
func (h *host) pumpPTY() {
	buf := make([]byte, 32*1024)
	for {
		n, err := h.cfg.PTY.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			h.cfg.Ring.Append(chunk)
			if frame, err := EncodeMessage(MsgTerminalData, chunk); err == nil {
				h.broadcast(frame)
			}
		}
		if err != nil {
			break
		}
	}

	// PTY reader is done (process exited or PTY closed). Wait for the Done
	// signal so ExitCode is populated before we send the status broadcast.
	<-h.cfg.PTY.Done()

	h.cfg.Ring.FlushPartial()

	code, _ := h.cfg.PTY.ExitCode()
	pid := h.cfg.PTY.PID()
	h.broadcast(statusFrame(false, pid, &code))
	// Keep-alive: do NOT shutdown here. The host stays up so clients can
	// still connect and read scrollback.
}

// broadcast sends msg to all connected clients, removing any that error.
func (h *host) broadcast(msg []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		if _, err := c.Write(msg); err != nil {
			_ = c.Close()
			delete(h.clients, c)
		}
	}
}

// sendTo sends msg to a single conn (best-effort; removes on error).
func (h *host) sendTo(conn net.Conn, msg []byte) {
	if _, err := conn.Write(msg); err != nil {
		h.mu.Lock()
		_ = conn.Close()
		delete(h.clients, conn)
		h.mu.Unlock()
	}
}

// handleConn manages the lifecycle of a single client connection.
func (h *host) handleConn(conn net.Conn) {
	// Scrollback replay: take the ring snapshot, write it to the conn, and add
	// the conn to the broadcast set all under a SINGLE h.mu hold. broadcast()
	// also takes h.mu, so it cannot interleave: any PTY chunk that arrives is
	// either already in this snapshot, or is broadcast strictly after the conn
	// joins the set. Doing this in two separate locks would let a chunk slip
	// into the gap (in neither the snapshot nor this client's broadcast) and be
	// silently dropped.
	// ponytail: the snapshot write happens while holding h.mu. It is bounded by
	// MaxOutputLines (the ring cap), so the lock hold is bounded; upgrade path
	// is a per-client send queue if a slow client ever stalls broadcast.
	h.mu.Lock()
	snap := h.cfg.Ring.Snapshot()
	if len(snap) > 0 {
		snapFrame, err := EncodeMessage(MsgTerminalData, snap)
		if err == nil {
			_, err = conn.Write(snapFrame)
		}
		if err != nil {
			h.mu.Unlock()
			_ = conn.Close()
			return
		}
	}
	h.clients[conn] = struct{}{}
	h.mu.Unlock()

	defer func() {
		h.mu.Lock()
		delete(h.clients, conn)
		h.mu.Unlock()
		_ = conn.Close()
	}()

	parser := NewMessageParser(func(msgType byte, payload []byte) {
		h.handleClientMsg(conn, msgType, payload)
	})

	buf := make([]byte, 4096)
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			parser.Feed(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

// handleClientMsg dispatches a decoded client message. Mirrors handleClientMessage
// from pty-host.ts.
func (h *host) handleClientMsg(conn net.Conn, msgType byte, payload []byte) {
	switch msgType {
	case MsgTerminalInput:
		if _, alive := h.cfg.PTY.ExitCode(); !alive {
			_, _ = h.cfg.PTY.Write(payload)
		}

	case MsgResize:
		if _, alive := h.cfg.PTY.ExitCode(); !alive {
			var rp ResizePayload
			if err := json.Unmarshal(payload, &rp); err == nil {
				_ = h.cfg.PTY.Resize(rp.Cols, rp.Rows)
			}
			// Malformed resize: ignore (matches TS behavior).
		}

	case MsgGetOutputReq:
		lines := 50 // default matches TS
		var req GetOutputReq
		if err := json.Unmarshal(payload, &req); err == nil && req.Lines > 0 {
			lines = req.Lines
		}
		text := h.cfg.Ring.Tail(lines)
		if frame, err := EncodeMessage(MsgGetOutputRes, []byte(text)); err == nil {
			h.sendTo(conn, frame)
		}

	case MsgStatusReq:
		code, exited := h.cfg.PTY.ExitCode()
		alive := !exited
		pid := h.cfg.PTY.PID()
		var codePtr *int
		if exited {
			codePtr = &code
		}
		h.sendTo(conn, statusFrame(alive, pid, codePtr))

	case MsgKillReq:
		// Trigger graceful shutdown; returns immediately (idempotent).
		go h.shutdown()
	}
}

// statusFrame builds a MsgStatusRes frame.
func statusFrame(alive bool, pid int, exitCode *int) []byte {
	sp := StatusPayload{Alive: alive, PID: pid, ExitCode: exitCode}
	b, _ := json.Marshal(sp)
	frame, _ := EncodeMessage(MsgStatusRes, b) // b is small JSON, never overflows uint32
	return frame
}
