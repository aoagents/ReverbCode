// Package panep ("pane ping") implements ports.AgentMessenger by typing a
// short pointer into the agent's runtime pane: "📥 ao: new message at
// .ao/inbox/<file> — please read it". The inbox messenger writes the file;
// panep merely tells the agent to read it. Multi-line message bodies typed
// verbatim fight with the agent's input handler, so a pointer is robust where
// pasting the full body is not.
//
// panep is best-effort: composite.Messenger logs and swallows panep errors so a
// missed nudge never loses the message — the inbox file is still on disk.
package panep

import (
	"context"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/messenger/inbox"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// SessionLookup resolves a session id to its runtime handle id and workspace
// path. The sqlite store satisfies this via a small adapter in
// daemon/session_wiring.go. The workspace path is required so panep can prove
// the inbox messenger had somewhere to write before pointing at the file.
type SessionLookup interface {
	SessionHandle(ctx context.Context, id domain.SessionID) (handleID, workspacePath string, err error)
}

// RuntimePaneWriter is the narrow runtime contract panep depends on: type
// characters into the pane identified by handle. Kept separate from
// ports.Runtime so adding a pane-ping does not widen the runtime port.
type RuntimePaneWriter interface {
	WriteChars(ctx context.Context, handle ports.RuntimeHandle, s string) error
}

// Messenger pings the agent's pane with a pointer at a freshly-written inbox
// file. It does not write the file itself — that is the inbox messenger's job.
type Messenger struct {
	runtime RuntimePaneWriter
	lookup  SessionLookup
	clock   func() time.Time
}

// New constructs a Messenger. Both deps are required.
func New(runtime RuntimePaneWriter, lookup SessionLookup) *Messenger {
	return &Messenger{runtime: runtime, lookup: lookup, clock: time.Now}
}

var _ ports.AgentMessenger = (*Messenger)(nil)

// Send pings the agent pane with a pointer to .ao/inbox/<file>. The filename
// is derived from the same (clock(), message) inputs the inbox messenger uses,
// so the two adapters agree on a single name when invoked together by the
// composite messenger.
func (m *Messenger) Send(ctx context.Context, id domain.SessionID, message string) error {
	handleID, ws, err := m.lookup.SessionHandle(ctx, id)
	if err != nil {
		return fmt.Errorf("panep: lookup handle for %s: %w", id, err)
	}
	if handleID == "" {
		return fmt.Errorf("panep: empty runtime handle for %s", id)
	}
	if ws == "" {
		return fmt.Errorf("panep: empty workspace path for %s", id)
	}

	// Reads the timestamp the composite messenger stashed via inbox.WithTime
	// so the filename here matches what the inbox messenger just wrote. Outside
	// a composite, falls back to m.clock — useful for tests and any future
	// caller that uses panep stand-alone.
	filename := inbox.FilenameFor(inbox.TimeFromContext(ctx, m.clock), message)
	body := fmt.Sprintf("📥 ao: new message at .ao/inbox/%s — please read it", filename)
	handle := ports.RuntimeHandle{ID: handleID}
	if err := m.runtime.WriteChars(ctx, handle, body); err != nil {
		return fmt.Errorf("panep: write ping for %s: %w", id, err)
	}
	if err := m.runtime.WriteChars(ctx, handle, "\n"); err != nil {
		return fmt.Errorf("panep: submit ping for %s: %w", id, err)
	}
	return nil
}
