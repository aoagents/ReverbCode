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
// that keeps fan-out, buffering, and re-attach testable without a real process:
// unit tests supply a scripted in-memory implementation; production uses a
// creack/pty-backed one (see pty_unix.go).
type ptyProcess interface {
	io.ReadWriteCloser
	Resize(rows, cols uint16) error
}

// spawnFunc starts a PTY for argv. ctx cancellation must terminate the process.
type spawnFunc func(ctx context.Context, argv []string) (ptyProcess, error)

// reattach policy: a PTY that drops is re-attached while the underlying Zellij
// session is still alive, up to maxReattach consecutive failures. An attach that
// survived longer than reattachResetGrace before dropping resets the counter, so
// a long-lived pane that blips recovers but a tight crash-loop gives up.
const (
	defaultMaxReattach       = 5
	defaultReattachResetTime = 5 * time.Second
)

// subscriber receives one terminal's output frames. It must not block: session
// fan-out calls subscribers while serializing replay/delivery under its mutex,
// so the WS layer funnels frames onto its own buffered writer.
type subscriber func(data []byte)

// session is one attached terminal pane, fanned out to N subscribers. It owns a
// single PTY (re-attached on drop) and a replay ring buffer.
type session struct {
	id     string
	handle ports.RuntimeHandle
	src    PTYSource
	spawn  spawnFunc
	log    *slog.Logger
	ring   *ringBuffer

	maxReattach int
	resetGrace  time.Duration

	mu       sync.Mutex
	pty      ptyProcess
	subs     map[int]subscriber
	exitSubs map[int]func()
	nextSub  int
	closed   bool
	exited   bool

	doneOnce sync.Once
	done     chan struct{}
}

func newSession(id string, handle ports.RuntimeHandle, src PTYSource, spawn spawnFunc, log *slog.Logger) *session {
	if log == nil {
		log = slog.Default()
	}
	return &session{
		id:          id,
		handle:      handle,
		src:         src,
		spawn:       spawn,
		log:         log,
		ring:        newRingBuffer(defaultRingMax),
		maxReattach: defaultMaxReattach,
		resetGrace:  defaultReattachResetTime,
		subs:        map[int]subscriber{},
		exitSubs:    map[int]func(){},
		done:        make(chan struct{}),
	}
}

// run drives attach → read-loop → re-attach until the pane exits cleanly, the
// session is closed, or ctx is cancelled. It is started once per session.
func (s *session) run(ctx context.Context) {
	defer s.markDone()

	failures := 0
	for {
		if s.isClosed() || ctx.Err() != nil {
			return
		}

		// Gate EVERY attach (including the first) on the runtime actually
		// being alive. `zellij attach` resurrects EXITED sessions — re-running
		// the serialized agent command — so attaching to a dead handle would
		// re-create a runtime the daemon already destroyed, outside lifecycle
		// control. A definitive "not alive" is a clean exit. A probe ERROR is
		// not proof of death: it retries with backoff up to the same
		// consecutive-failure cap as attach failures.
		alive, err := s.src.IsAlive(ctx, s.handle)
		if err != nil {
			failures++
			if failures > s.maxReattach {
				s.fail("liveness probe: " + err.Error())
				return
			}
			if !s.backoff(ctx, failures) {
				return
			}
			continue
		}
		if !alive {
			s.markExited()
			return
		}

		argv, err := s.src.AttachCommand(s.handle)
		if err != nil {
			s.fail("attach command: " + err.Error())
			return
		}
		p, err := s.spawn(ctx, argv)
		if err != nil {
			failures++
			if failures > s.maxReattach {
				s.fail("spawn pty: " + err.Error())
				return
			}
			if !s.backoff(ctx, failures) {
				return
			}
			continue
		}

		s.setPTY(p)
		start := time.Now()
		s.copyOut(p)
		_ = p.Close()

		if time.Since(start) >= s.resetGrace {
			failures = 0
		}
		failures++

		if failures > s.maxReattach {
			s.markExited()
			return
		}
		if !s.backoff(ctx, failures) {
			return
		}
		s.log.Debug("terminal re-attaching", "id", s.id, "failures", failures)
	}
}

// copyOut pumps PTY output into the ring buffer and out to subscribers until the
// PTY closes or errors.
func (s *session) copyOut(p ptyProcess) {
	buf := make([]byte, 32*1024)
	for {
		n, err := p.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			s.deliver(chunk)
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
func (s *session) backoff(ctx context.Context, failures int) bool {
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

// subscribe registers an output callback and an exit callback, replays the ring
// buffer to the new subscriber, and returns an unsubscribe func. If the pane has
// already exited, onExit fires immediately and exited is true; the caller must
// not treat the returned no-op unsubscribe as a live registration (there is
// nothing to track and re-opening must stay possible).
func (s *session) subscribe(onData subscriber, onExit func()) (unsubscribe func(), exited bool) {
	s.mu.Lock()
	if s.exited {
		s.mu.Unlock()
		if onExit != nil {
			onExit()
		}
		return func() {}, true
	}
	id := s.nextSub
	s.nextSub++
	s.subs[id] = onData
	if onExit != nil {
		s.exitSubs[id] = onExit
	}
	// Deliver the replay while still holding s.mu. deliver (the copyOut path)
	// also takes s.mu around append+fanout, so the two are fully serialized: a
	// chunk is either in this snapshot (and was fanned out before this
	// subscriber registered) or it is fanned out after this returns, never both.
	// Releasing the lock before replaying would let a chunk land in both the
	// snapshot and a concurrent fanout, delivering it twice (or let an exit
	// frame overtake the replay). onData is a non-blocking enqueue, so holding
	// the lock across it cannot deadlock.
	replay := s.ring.snapshot()
	if len(replay) > 0 {
		onData(replay)
	}
	s.mu.Unlock()

	return func() {
		s.mu.Lock()
		delete(s.subs, id)
		delete(s.exitSubs, id)
		s.mu.Unlock()
	}, false
}

// deliver appends a chunk to the ring and fans it out to current subscribers as
// one atomic step under s.mu. Holding the lock across both is what lets
// subscribe (which snapshots + replays under the same lock) guarantee
// exactly-once delivery: append+fanout and register+snapshot+replay can never
// interleave. Each fn is a non-blocking enqueue, so the lock is held only
// briefly and cannot deadlock.
func (s *session) deliver(chunk []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ring.append(chunk)
	for _, fn := range s.subs {
		fn(chunk)
	}
}

// write sends client keystrokes to the PTY. It is a no-op if no PTY is attached.
func (s *session) write(p []byte) error {
	s.mu.Lock()
	pty := s.pty
	s.mu.Unlock()
	if pty == nil {
		return errors.New("terminal: no active pty")
	}
	_, err := pty.Write(p)
	return err
}

func (s *session) resize(rows, cols uint16) error {
	s.mu.Lock()
	pty := s.pty
	s.mu.Unlock()
	if pty == nil {
		return nil
	}
	return pty.Resize(rows, cols)
}

func (s *session) setPTY(p ptyProcess) {
	s.mu.Lock()
	s.pty = p
	s.mu.Unlock()
}

// close tears the session down: stop re-attaching and kill the PTY.
func (s *session) close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	pty := s.pty
	s.pty = nil
	s.mu.Unlock()
	if pty != nil {
		_ = pty.Close()
	}
}

func (s *session) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *session) isExited() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exited
}

// markExited flips the pane to exited and notifies/clears subscribers.
func (s *session) markExited() {
	s.mu.Lock()
	if s.exited {
		s.mu.Unlock()
		return
	}
	s.exited = true
	exits := make([]func(), 0, len(s.exitSubs))
	for _, fn := range s.exitSubs {
		exits = append(exits, fn)
	}
	s.exitSubs = map[int]func(){}
	s.mu.Unlock()
	for _, fn := range exits {
		fn()
	}
}

// fail reports an unrecoverable attach error to subscribers as an exit.
func (s *session) fail(reason string) {
	s.log.Warn("terminal session failed", "id", s.id, "reason", reason)
	s.markExited()
}

func (s *session) markDone() { s.doneOnce.Do(func() { close(s.done) }) }
