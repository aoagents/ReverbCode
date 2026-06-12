package terminal

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// PTYSource is what a terminal needs from the runtime: the argv that attaches a
// PTY to a session's pane, and a liveness check used to decide whether a dropped
// PTY should be re-attached or treated as a clean exit. The Zellij runtime adapter
// satisfies this via AttachCommand/IsAlive; the interface lives here, next to its
// only consumer, so terminal does not depend on a concrete adapter.
type PTYSource interface {
	AttachCommand(handle ports.RuntimeHandle) ([]string, error)
	IsAlive(ctx context.Context, handle ports.RuntimeHandle) (bool, error)
}

// ptyProcess is a started PTY-backed attach process. It is the injection seam
// that keeps the attach loop testable without a real process: unit tests supply
// a scripted in-memory implementation; production uses a creack/pty-backed one
// (see pty_unix.go).
type ptyProcess interface {
	io.ReadWriteCloser
	Resize(rows, cols uint16) error
}

// spawnFunc starts a PTY for argv, sized rows×cols from the start (zero means
// no size was recorded yet — kernel default). Spawning at the client's grid
// matters: the attach process reads the tty size once at startup, and sizing
// the PTY only after exec relies on SIGWINCH delivery that can race the
// process installing its handler — a missed signal leaves the zellij client
// laid out for a stale size. ctx cancellation must terminate the process.
type spawnFunc func(ctx context.Context, argv []string, rows, cols uint16) (ptyProcess, error)

// reattach policy: a PTY that drops is re-attached while the underlying Zellij
// session is still alive, up to maxReattach consecutive failures. An attach that
// survived longer than reattachResetGrace before dropping resets the counter, so
// a long-lived pane that blips recovers but a tight crash-loop gives up.
const (
	defaultMaxReattach       = 5
	defaultReattachResetTime = 5 * time.Second
)

// attachment is ONE client's hold on a pane: a private `zellij attach` PTY
// spawned per mux open, streaming to a single sink. Zellij is the multiplexer —
// it owns the session's screen state and scrollback, and answers every fresh
// attach with its full init handshake (alt screen, SGR mouse tracking,
// bracketed paste) followed by a faithful repaint. That handshake is why the
// PTY is per-client and there is no server-side replay buffer: a byte ring
// can replay recent output, but the one-time mode negotiation at the head of
// the stream scrolls out of any bounded buffer, leaving late subscribers
// without mouse reporting (wheel scroll dead). A fresh attach per client makes
// Zellij re-send it, every time, by construction.
//
// onData must not block: the WS layer funnels frames onto its own buffered
// writer. onExit fires at most once, when the attach loop gives up (runtime
// dead, attach failure cap) — never on close().
type attachment struct {
	id     string
	handle ports.RuntimeHandle
	src    PTYSource
	spawn  spawnFunc
	log    *slog.Logger
	onData func(data []byte)
	onExit func()

	maxReattach int
	resetGrace  time.Duration

	mu     sync.Mutex
	pty    ptyProcess
	rows   uint16 // last size the client asked for; re-applied on every attach
	cols   uint16
	closed bool
	exited bool
}

func newAttachment(id string, handle ports.RuntimeHandle, src PTYSource, spawn spawnFunc, onData func([]byte), onExit func(), log *slog.Logger) *attachment {
	if log == nil {
		log = slog.Default()
	}
	if onData == nil {
		onData = func([]byte) {}
	}
	return &attachment{
		id:          id,
		handle:      handle,
		src:         src,
		spawn:       spawn,
		log:         log,
		onData:      onData,
		onExit:      onExit,
		maxReattach: defaultMaxReattach,
		resetGrace:  defaultReattachResetTime,
	}
}

// run drives attach → read-loop → re-attach until the pane exits cleanly, the
// attachment is closed, or ctx is cancelled. It is started once per attachment.
func (a *attachment) run(ctx context.Context) {
	failures := 0
	for {
		if a.isClosed() || ctx.Err() != nil {
			return
		}

		// Gate EVERY attach (including the first) on the runtime actually
		// being alive. `zellij attach` resurrects EXITED sessions — re-running
		// the serialized agent command — so attaching to a dead handle would
		// re-create a runtime the daemon already destroyed, outside lifecycle
		// control. A definitive "not alive" is a clean exit. A probe ERROR is
		// not proof of death: it retries with backoff up to the same
		// consecutive-failure cap as attach failures.
		alive, err := a.src.IsAlive(ctx, a.handle)
		if err != nil {
			failures++
			if failures > a.maxReattach {
				a.fail("liveness probe: " + err.Error())
				return
			}
			if !a.backoff(ctx, failures) {
				return
			}
			continue
		}
		if !alive {
			a.markExited()
			return
		}

		argv, err := a.src.AttachCommand(a.handle)
		if err != nil {
			a.fail("attach command: " + err.Error())
			return
		}
		rows, cols := a.size()
		p, err := a.spawn(ctx, argv, rows, cols)
		if err != nil {
			failures++
			if failures > a.maxReattach {
				a.fail("spawn pty: " + err.Error())
				return
			}
			if !a.backoff(ctx, failures) {
				return
			}
			continue
		}

		a.setPTY(p)
		start := time.Now()
		a.copyOut(p)
		_ = p.Close()

		if time.Since(start) >= a.resetGrace {
			failures = 0
		}
		failures++

		if failures > a.maxReattach {
			a.markExited()
			return
		}
		if !a.backoff(ctx, failures) {
			return
		}
		a.log.Debug("terminal re-attaching", "id", a.id, "failures", failures)
	}
}

// copyOut pumps PTY output to the sink until the PTY closes or errors.
func (a *attachment) copyOut(p ptyProcess) {
	buf := make([]byte, 32*1024)
	for {
		n, err := p.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			a.onData(chunk)
		}
		if err != nil {
			return
		}
	}
}

// backoff sleeps between attach attempts; false means ctx was cancelled.
// Whether another attempt is warranted at all (liveness, failure cap) is
// decided at the top of the run loop, so a re-attach and a first attach share
// one gate.
func (a *attachment) backoff(ctx context.Context, failures int) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(reattachBackoff(failures)):
		return true
	}
}

func reattachBackoff(failures int) time.Duration {
	d := time.Duration(failures) * 200 * time.Millisecond
	if d > time.Second {
		d = time.Second
	}
	return d
}

// write sends client keystrokes to the PTY. It is a no-op if no PTY is attached.
func (a *attachment) write(p []byte) error {
	a.mu.Lock()
	pty := a.pty
	a.mu.Unlock()
	if pty == nil {
		return errors.New("terminal: no active pty")
	}
	_, err := pty.Write(p)
	return err
}

// resize records the client's grid and applies it to the live PTY. The size is
// remembered so an attach that is still in flight (or a later re-attach) starts
// at the client's grid instead of the kernel default — the open frame's
// cols/rows land here before the PTY exists.
func (a *attachment) resize(rows, cols uint16) error {
	a.mu.Lock()
	a.rows, a.cols = rows, cols
	pty := a.pty
	a.mu.Unlock()
	if pty == nil {
		return nil
	}
	return pty.Resize(rows, cols)
}

// size returns the client's last requested grid (zero before the first
// open/resize recorded one). The spawn path reads it so the PTY starts at the
// client's grid instead of the kernel default.
func (a *attachment) size() (rows, cols uint16) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.rows, a.cols
}

// setPTY publishes a freshly attached PTY and replays the client's last
// requested size onto it (see resize) — the spawn already started at the size
// read in run, but a resize frame can land between that read and registration
// here; the replay (Setsize + explicit WINCH) converges the late case.
func (a *attachment) setPTY(p ptyProcess) {
	a.mu.Lock()
	a.pty = p
	rows, cols := a.rows, a.cols
	a.mu.Unlock()
	if rows > 0 && cols > 0 {
		_ = p.Resize(rows, cols)
	}
}

// close detaches this client: stop re-attaching and kill the attach PTY. It
// never touches the Zellij session itself, which the zellij server keeps alive
// for other clients.
func (a *attachment) close() {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return
	}
	a.closed = true
	pty := a.pty
	a.pty = nil
	a.mu.Unlock()
	if pty != nil {
		_ = pty.Close()
	}
}

func (a *attachment) isClosed() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.closed
}

func (a *attachment) isExited() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.exited
}

// markExited flips the attachment to exited and fires onExit once.
func (a *attachment) markExited() {
	a.mu.Lock()
	if a.exited {
		a.mu.Unlock()
		return
	}
	a.exited = true
	a.mu.Unlock()
	if a.onExit != nil {
		a.onExit()
	}
}

// fail reports an unrecoverable attach error as an exit.
func (a *attachment) fail(reason string) {
	a.log.Warn("terminal attachment failed", "id", a.id, "reason", reason)
	a.markExited()
}
