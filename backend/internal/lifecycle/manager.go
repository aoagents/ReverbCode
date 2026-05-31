// Package lifecycle implements ports.LifecycleManager: the synchronous reducer
// that writes durable session facts. It deliberately keeps the session model
// small: activity_state plus is_terminated are the status-like facts persisted on
// the session row; display status is derived on read with PR facts.
package lifecycle

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Manager reduces runtime, activity, PR, spawn, and kill observations into durable session facts and reactions.
type Manager struct {
	store     ports.SessionStore
	pr        ports.PRWriter
	notifier  ports.Notifier
	messenger ports.AgentMessenger

	mu     sync.Mutex
	window time.Duration
	clock  func() time.Time

	react reactionState
}

var _ ports.LifecycleManager = (*Manager)(nil)

// New builds a Lifecycle Manager over its collaborators: the session store it
// is the sole writer of, the PR-facts writer, the notifier, and the messenger
// used to nudge running agents.
func New(store ports.SessionStore, pr ports.PRWriter, notifier ports.Notifier, messenger ports.AgentMessenger) *Manager {
	return &Manager{store: store, pr: pr, notifier: notifier, messenger: messenger, window: defaultRecentActivityWindow, clock: time.Now, react: newReactionState()}
}

func (m *Manager) mutate(ctx context.Context, id domain.SessionID, fn func(domain.SessionRecord) (domain.SessionRecord, bool)) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil || !ok {
		return false, err
	}
	next, changed := fn(rec)
	if !changed {
		return false, nil
	}
	next.UpdatedAt = m.clock()
	if err := m.store.UpdateSession(ctx, next); err != nil {
		return false, err
	}
	return true, nil
}

// ApplyRuntimeObservation only writes when runtime liveness is unambiguous. A
// failed/unknown probe or disagreement is ignored; no transient lifecycle state is stored.
func (m *Manager) ApplyRuntimeObservation(ctx context.Context, id domain.SessionID, f ports.RuntimeFacts) error {
	changed, err := m.mutate(ctx, id, func(cur domain.SessionRecord) (domain.SessionRecord, bool) {
		if cur.IsTerminated {
			return cur, false
		}
		next := cur
		if runtimeClearlyDead(f, cur.Activity, m.window) {
			next.IsTerminated = true
			next.Activity = domain.ActivitySubstate{State: domain.ActivityExited, LastActivityAt: nowOr(f.ObservedAt), Source: domain.SourceRuntime}
			return next, true
		}
		if runtimeClearlyAlive(f) && cur.Activity.State == domain.ActivityExited {
			next.Activity = domain.ActivitySubstate{State: domain.ActivityReady, LastActivityAt: nowOr(f.ObservedAt), Source: domain.SourceRuntime}
			return next, true
		}
		return cur, false
	})
	if err != nil || !changed {
		return err
	}
	return m.runReactions(ctx, id, reactionContent{})
}

// ApplyActivitySignal records an authoritative agent activity signal and runs reactions.
func (m *Manager) ApplyActivitySignal(ctx context.Context, id domain.SessionID, s ports.ActivitySignal) error {
	if !s.Valid {
		return nil
	}
	changed, err := m.mutate(ctx, id, func(cur domain.SessionRecord) (domain.SessionRecord, bool) {
		if cur.IsTerminated {
			return cur, false
		}
		next := cur
		act := domain.ActivitySubstate{State: s.State, LastActivityAt: nowOr(s.Timestamp), Source: s.Source}
		if sameActivity(cur.Activity, act) {
			return cur, false
		}
		next.Activity = act
		if s.State == domain.ActivityExited {
			next.IsTerminated = true
		}
		return next, true
	})
	if err != nil || !changed {
		return err
	}
	return m.runReactions(ctx, id, reactionContent{})
}

// ApplyPRObservation records fetched PR facts and runs PR-driven reactions.
func (m *Manager) ApplyPRObservation(ctx context.Context, id domain.SessionID, o ports.PRObservation) error {
	if !o.Fetched {
		return nil
	}
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil || !ok {
		return err
	}
	if err := m.writePR(ctx, id, o); err != nil {
		return err
	}
	if o.Merged {
		changed, err := m.mutate(ctx, id, func(cur domain.SessionRecord) (domain.SessionRecord, bool) {
			if cur.IsTerminated {
				return cur, false
			}
			cur.IsTerminated = true
			cur.Activity = domain.ActivitySubstate{State: domain.ActivityExited, LastActivityAt: m.clock(), Source: domain.SourceRuntime}
			return cur, true
		})
		if err != nil {
			return err
		}
		if changed {
			m.clearReactions(id)
			return m.fireNotify(ctx, id, rec.ProjectID, rxMerged, reactions[rxMerged])
		}
		return nil
	}
	return m.runReactions(ctx, id, prContent(o))
}

func (m *Manager) writePR(ctx context.Context, id domain.SessionID, o ports.PRObservation) error {
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
	return m.pr.WritePR(ctx, row, checks, comments)
}

// OnSpawnCompleted marks a newly spawned or restored session live and stores runtime/workspace handles.
func (m *Manager) OnSpawnCompleted(ctx context.Context, id domain.SessionID, o ports.SpawnOutcome) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("lifecycle: OnSpawnCompleted for unknown session %q", id)
	}
	rec.IsTerminated = false
	rec.Activity = domain.ActivitySubstate{State: domain.ActivityReady, LastActivityAt: m.clock(), Source: domain.SourceRuntime}
	rec.Metadata = mergeMetadata(rec.Metadata, spawnMetadata(o))
	rec.UpdatedAt = m.clock()
	return m.store.UpdateSession(ctx, rec)
}

// OnKillRequested marks a session terminated after an explicit kill request.
func (m *Manager) OnKillRequested(ctx context.Context, id domain.SessionID) error {
	_, err := m.mutate(ctx, id, func(cur domain.SessionRecord) (domain.SessionRecord, bool) {
		if cur.IsTerminated {
			return cur, false
		}
		cur.IsTerminated = true
		cur.Activity = domain.ActivitySubstate{State: domain.ActivityExited, LastActivityAt: m.clock(), Source: domain.SourceRuntime}
		return cur, true
	})
	m.clearReactions(id)
	return err
}

// RunningSessions returns every session that still needs runtime liveness probes.
func (m *Manager) RunningSessions(ctx context.Context) ([]domain.SessionRecord, error) {
	all, err := m.store.ListAllSessions(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]domain.SessionRecord, 0, len(all))
	for _, rec := range all {
		if !rec.IsTerminated {
			out = append(out, rec)
		}
	}
	return out, nil
}

func sameActivity(a, b domain.ActivitySubstate) bool {
	return a.State == b.State && a.Source == b.Source && a.LastActivityAt.Equal(b.LastActivityAt)
}

func spawnMetadata(o ports.SpawnOutcome) domain.SessionMetadata {
	return domain.SessionMetadata{Branch: o.Branch, WorkspacePath: o.WorkspacePath, RuntimeHandleID: o.RuntimeHandle.ID, AgentSessionID: o.AgentSessionID, Prompt: o.Prompt}
}

func mergeMetadata(base, in domain.SessionMetadata) domain.SessionMetadata {
	set := func(dst *string, v string) {
		if v != "" {
			*dst = v
		}
	}
	set(&base.Branch, in.Branch)
	set(&base.WorkspacePath, in.WorkspacePath)
	set(&base.RuntimeHandleID, in.RuntimeHandleID)
	set(&base.AgentSessionID, in.AgentSessionID)
	set(&base.Prompt, in.Prompt)
	return base
}
