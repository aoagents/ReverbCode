// Package pr records SCM observations for pull requests associated with sessions.
package pr

import (
	"context"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type sessionReader interface {
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
}

type lifecycle interface {
	MarkTerminated(ctx context.Context, id domain.SessionID) error
}

// Manager persists PR observations and applies the few session lifecycle effects
// that directly follow from PR state, such as terminating a session whose PR merged.
type Manager struct {
	sessions  sessionReader
	writer    ports.PRWriter
	lifecycle lifecycle
	clock     func() time.Time
}

// Deps are the collaborators a PR Manager needs.
type Deps struct {
	Sessions  sessionReader
	Writer    ports.PRWriter
	Lifecycle lifecycle
	Clock     func() time.Time
}

// New builds a PR Manager from its dependencies, defaulting the clock to time.Now.
func New(d Deps) *Manager {
	m := &Manager{sessions: d.Sessions, writer: d.Writer, lifecycle: d.Lifecycle, clock: d.Clock}
	if m.clock == nil {
		m.clock = time.Now
	}
	return m
}

// ApplyObservation records a successfully fetched PR observation. Failed fetches
// are ignored because their fields are not authoritative facts.
func (m *Manager) ApplyObservation(ctx context.Context, id domain.SessionID, o ports.PRObservation) error {
	if !o.Fetched {
		return nil
	}
	if m.sessions != nil {
		_, ok, err := m.sessions.GetSession(ctx, id)
		if err != nil || !ok {
			return err
		}
	}
	if err := m.write(ctx, id, o); err != nil {
		return err
	}
	if o.Merged && m.lifecycle != nil {
		if err := m.lifecycle.MarkTerminated(ctx, id); err != nil {
			return fmt.Errorf("terminate merged session %s: %w", id, err)
		}
	}
	return nil
}

func (m *Manager) write(ctx context.Context, id domain.SessionID, o ports.PRObservation) error {
	now := m.clock()
	row := domain.PRRow{URL: o.URL, SessionID: id, Number: o.Number, Draft: o.Draft, Merged: o.Merged, Closed: o.Closed, CI: o.CI, Review: o.Review, Mergeability: o.Mergeability, UpdatedAt: now}
	checks := make([]domain.PRCheckRow, len(o.Checks))
	for i, c := range o.Checks {
		c.PRURL = o.URL
		if c.CreatedAt.IsZero() {
			c.CreatedAt = now
		}
		checks[i] = c
	}
	comments := make([]domain.PRComment, len(o.Comments))
	for i, c := range o.Comments {
		if c.CreatedAt.IsZero() {
			c.CreatedAt = now
		}
		comments[i] = c
	}
	return m.writer.WritePR(ctx, row, checks, comments)
}
