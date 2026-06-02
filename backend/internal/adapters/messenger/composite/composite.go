// Package composite fans Send out to a primary AgentMessenger followed by
// best-effort secondaries. The primary's failure aborts the whole call (the
// message was never delivered); a secondary's failure is logged and swallowed
// (the message IS delivered — the secondary was a live nudge, not a record).
//
// This is how the daemon wires the inbox messenger (primary, durable file
// write) with the pane-ping messenger (secondary, live zellij nudge): if we
// could not write the file we must not tell the agent about a file that does
// not exist, but if we wrote the file and could not ping, the agent will see
// it on the next inbox check.
package composite

import (
	"context"
	"io"
	"log/slog"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/messenger/inbox"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Messenger fans Send out across Inner messengers with primary-then-secondary
// semantics. Inner[0] is the primary (must succeed); Inner[1:] are best-effort
// secondaries whose failures are logged at WARN and swallowed. Inner is public
// so wiring tests can assert the daemon assembles the composite with the
// expected ordering (inbox first, panep second).
//
// Each Send pins one timestamp via inbox.WithTime so inner messengers that
// derive a filename from (t, message) agree on the same name — without this,
// the inbox file write and a downstream panep ping would land on different
// nanoseconds and the ping would point at a file that does not exist.
type Messenger struct {
	Inner  []ports.AgentMessenger
	Logger *slog.Logger
	Clock  func() time.Time
}

// New builds a Messenger. A nil logger is replaced with a discard logger so a
// secondary failure never panics a misconfigured caller. Clock defaults to
// time.Now.
func New(inner []ports.AgentMessenger, logger *slog.Logger) *Messenger {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Messenger{Inner: inner, Logger: logger, Clock: time.Now}
}

var _ ports.AgentMessenger = (*Messenger)(nil)

// Send invokes Inner[0] (primary); on failure, no secondaries run and the
// error is returned. On primary success, every Inner[1:] is invoked in order
// and their errors are logged at WARN.
func (c *Messenger) Send(ctx context.Context, id domain.SessionID, message string) error {
	if len(c.Inner) == 0 {
		return nil
	}
	ctx = inbox.WithTime(ctx, c.Clock())
	if err := c.Inner[0].Send(ctx, id, message); err != nil {
		return err
	}
	for _, m := range c.Inner[1:] {
		if err := m.Send(ctx, id, message); err != nil {
			c.Logger.Warn("composite: secondary messenger failed", "sessionId", id, "err", err)
		}
	}
	return nil
}
