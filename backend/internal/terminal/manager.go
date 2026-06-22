package terminal

import (
	"context"
	"encoding/base64"
	"log/slog"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/cdc"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// EventSource is the session-state feed the "sessions" channel forwards. The CDC
// broadcaster satisfies it; the interface lives next to its consumer so terminal
// does not depend on CDC internals beyond the Event shape.
type EventSource interface {
	Subscribe(fn func(cdc.Event)) (unsubscribe func())
}

// wsConn is the transport seam: a JSON-framed, single-reader/single-writer
// WebSocket connection. internal/httpd adapts coder/websocket to this; tests
// supply an in-memory fake. WriteJSON is only ever called from the per-conn
// writer goroutine; Ping may be called concurrently (it is a control frame).
type wsConn interface {
	ReadJSON(ctx context.Context, v any) error
	WriteJSON(ctx context.Context, v any) error
	Ping(ctx context.Context) error
	Close(reason string) error
}

const (
	defaultHeartbeat   = 15 * time.Second
	defaultWriteBuffer = 1024
)

// Manager serves WebSocket clients, spawning one attach PTY per opened pane
// per connection. There is no shared per-pane state to outlive a connection:
// the Zellij server owns the session (screen, scrollback, modes), and every
// fresh attach gets its full handshake + repaint. A client reconnect simply
// attaches again.
type Manager struct {
	src       PTYSource
	events    EventSource
	spawn     spawnFunc
	log       *slog.Logger
	heartbeat time.Duration

	// ctx scopes every attachment's PTY lifetime; cancelled by Close.
	ctx    context.Context
	cancel context.CancelFunc

	mu          sync.Mutex
	attachments map[*attachment]struct{}
	closed      bool
}

// Option configures a Manager.
type Option func(*Manager)

// WithSpawn overrides the PTY spawner (tests inject a fake).
func WithSpawn(fn spawnFunc) Option { return func(m *Manager) { m.spawn = fn } }

// WithHeartbeat overrides the ping interval.
func WithHeartbeat(d time.Duration) Option { return func(m *Manager) { m.heartbeat = d } }

// NewManager builds a Manager. src attaches PTYs; events feeds the session
// channel (may be nil to disable it). A nil logger falls back to slog.Default.
func NewManager(src PTYSource, events EventSource, log *slog.Logger, opts ...Option) *Manager {
	if log == nil {
		log = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{
		src:         src,
		events:      events,
		spawn:       defaultSpawn,
		log:         log,
		heartbeat:   defaultHeartbeat,
		ctx:         ctx,
		cancel:      cancel,
		attachments: map[*attachment]struct{}{},
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// Close tears down every live attachment and stops re-attach loops. Safe to
// call once on daemon shutdown.
func (m *Manager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	attachments := make([]*attachment, 0, len(m.attachments))
	for a := range m.attachments {
		attachments = append(attachments, a)
	}
	m.attachments = map[*attachment]struct{}{}
	m.mu.Unlock()

	m.cancel()
	for _, a := range attachments {
		a.close()
	}
}

// track registers a live attachment so Close can tear it down; it refuses new
// attachments once the manager is closed.
func (m *Manager) track(a *attachment) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return context.Canceled
	}
	m.attachments[a] = struct{}{}
	return nil
}

func (m *Manager) forget(a *attachment) {
	m.mu.Lock()
	delete(m.attachments, a)
	m.mu.Unlock()
}

// Serve runs the protocol loop for one client connection until it errors, the
// client disconnects, or ctx/the manager is cancelled. It owns the single writer
// goroutine and the heartbeat.
func (m *Manager) Serve(ctx context.Context, conn wsConn) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	c := &connState{
		mgr:    m,
		conn:   conn,
		cancel: cancel,
		out:    make(chan serverMsg, defaultWriteBuffer),
		terms:  map[string]*attachment{},
	}
	defer c.cleanup()

	go c.writeLoop(ctx)
	go c.heartbeatLoop(ctx, m.heartbeat)

	for {
		var msg clientMsg
		if err := conn.ReadJSON(ctx, &msg); err != nil {
			return
		}
		if ctx.Err() != nil {
			return
		}
		c.handle(msg)
	}
}

// connState is the per-connection mutable state.
type connState struct {
	mgr    *Manager
	conn   wsConn
	cancel context.CancelFunc
	out    chan serverMsg

	mu        sync.Mutex
	terms     map[string]*attachment // terminal id -> this conn's own attach PTY
	unsubEvts func()
	closed    bool
}

func (c *connState) handle(msg clientMsg) {
	switch msg.Ch {
	case chTerminal:
		c.handleTerminal(msg)
	case chSubscribe:
		c.handleSubscribe(msg)
	case chSystem:
		if msg.Type == msgPing {
			c.enqueue(serverMsg{Ch: chSystem, Type: msgPong})
		}
	}
}

func (c *connState) handleTerminal(msg clientMsg) {
	switch msg.Type {
	case msgOpen:
		c.openTerminal(msg.ID, msg.Rows, msg.Cols)
	case msgData:
		raw, err := base64.StdEncoding.DecodeString(msg.Data)
		if err != nil {
			return
		}
		if a := c.lookup(msg.ID); a != nil {
			_ = a.write(raw)
		}
	case msgResize:
		if a := c.lookup(msg.ID); a != nil {
			_ = a.resize(msg.Rows, msg.Cols)
		}
	case msgClose:
		c.closeTerminal(msg.ID)
	}
}

// openTerminal spawns this connection's own attach PTY for the pane. rows/cols
// are the client's grid from the open frame, applied as the PTY's initial size
// (a resize that raced ahead of the attach would otherwise be lost).
func (c *connState) openTerminal(id string, rows, cols uint16) {
	if id == "" {
		c.enqueue(serverMsg{Ch: chTerminal, Type: msgError, Error: "missing terminal id"})
		return
	}
	c.mu.Lock()
	if _, ok := c.terms[id]; ok {
		c.mu.Unlock()
		return // already open on this conn; avoid a duplicate attach
	}
	c.mu.Unlock()

	// a is captured by onExit before assignment; safe because the attach loop —
	// the only thing that fires onExit — starts after the registration below.
	var a *attachment
	a = newAttachment(id, ports.RuntimeHandle{ID: id}, c.mgr.src, c.mgr.spawn,
		func() {
			c.enqueue(serverMsg{Ch: chTerminal, ID: id, Type: msgOpened})
		},
		func(data []byte) {
			c.enqueue(serverMsg{
				Ch:   chTerminal,
				ID:   id,
				Type: msgData,
				Data: base64.StdEncoding.EncodeToString(data),
			})
		},
		func() {
			// Clear the connection's entry for this id before sending exited so
			// a client that reopens the moment it sees exited finds no stale
			// entry and is served instead of dropped by the open guard. Guard on
			// identity: that reopen may already have installed a fresh
			// attachment under the same id, which must not be evicted.
			c.mu.Lock()
			if c.terms[id] == a {
				delete(c.terms, id)
			}
			c.mu.Unlock()
			c.enqueue(serverMsg{Ch: chTerminal, ID: id, Type: msgExited})
		},
		c.mgr.log)
	if rows > 0 && cols > 0 {
		_ = a.resize(rows, cols) // recorded now, applied when the PTY attaches
	}
	if err := c.mgr.track(a); err != nil {
		c.enqueue(serverMsg{Ch: chTerminal, ID: id, Type: msgError, Error: err.Error()})
		return
	}
	c.mu.Lock()
	c.terms[id] = a
	c.mu.Unlock()

	go func() {
		a.run(c.mgr.ctx)
		c.mgr.forget(a)
	}()
}

func (c *connState) closeTerminal(id string) {
	c.mu.Lock()
	a := c.terms[id]
	delete(c.terms, id)
	c.mu.Unlock()
	if a != nil {
		a.close()
	}
}

func (c *connState) lookup(id string) *attachment {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.terms[id]
}

func (c *connState) handleSubscribe(msg clientMsg) {
	if msg.Type != msgSubscribe || c.mgr.events == nil {
		return
	}
	c.mu.Lock()
	if c.unsubEvts != nil {
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()

	unsub := c.mgr.events.Subscribe(func(e cdc.Event) {
		c.enqueue(serverMsg{
			Ch:   chSessions,
			Type: msgSnapshot,
			Session: &sessionUpdate{
				Seq:       e.Seq,
				ProjectID: e.ProjectID,
				SessionID: e.SessionID,
				EventType: string(e.Type),
			},
		})
	})
	c.mu.Lock()
	c.unsubEvts = unsub
	c.mu.Unlock()
}

// enqueue pushes a frame to the writer. If the buffer is full the client is too
// slow to keep up; tear the connection down rather than block the attachment's
// PTY read loop behind it.
func (c *connState) enqueue(msg serverMsg) {
	select {
	case c.out <- msg:
	default:
		c.cancel()
	}
}

func (c *connState) writeLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-c.out:
			if err := c.conn.WriteJSON(ctx, msg); err != nil {
				c.cancel()
				return
			}
		}
	}
}

func (c *connState) heartbeatLoop(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			pctx, cancel := context.WithTimeout(ctx, interval)
			err := c.conn.Ping(pctx)
			cancel()
			if err != nil {
				c.cancel()
				return
			}
		}
	}
}

func (c *connState) cleanup() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	attachments := make([]*attachment, 0, len(c.terms))
	for _, a := range c.terms {
		attachments = append(attachments, a)
	}
	c.terms = map[string]*attachment{}
	unsubEvts := c.unsubEvts
	c.unsubEvts = nil
	c.mu.Unlock()

	for _, a := range attachments {
		a.close()
	}
	if unsubEvts != nil {
		unsubEvts()
	}
	_ = c.conn.Close("server: connection closed")
}
