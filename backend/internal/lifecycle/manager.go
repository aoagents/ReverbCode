// Package lifecycle implements the synchronous reducer that writes durable
// session lifecycle facts. It deliberately keeps the session model small:
// activity_state plus an is_terminated bit are the only persisted status-like
// facts on the session row.
package lifecycle

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type sessionStore interface {
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
	UpdateSession(ctx context.Context, rec domain.SessionRecord) error
	// GetPRLastNudgeSignature / UpdatePRLastNudgeSignature persist the
	// reaction-dedup map so nudges survive a daemon restart.
	GetPRLastNudgeSignature(ctx context.Context, prURL string) (string, error)
	UpdatePRLastNudgeSignature(ctx context.Context, prURL, payload string) error
}

type notificationSink interface {
	Notify(ctx context.Context, intent domain.NotificationIntent) error
}

// Deps are the explicit collaborators used by Manager. Notifications is
// optional so tests and transitional wiring can keep using New.
type Deps struct {
	Store         sessionStore
	Messenger     ports.AgentMessenger
	Notifications notificationSink
}

// Manager reduces runtime, activity, spawn, and termination observations into durable session facts.
// It also owns agent nudges caused by PR observations, including merge-conflict, CI-failure, and review-feedback prompts.
type Manager struct {
	store         sessionStore
	messenger     ports.AgentMessenger
	notifications notificationSink

	mu     sync.Mutex
	window time.Duration
	clock  func() time.Time
	react  reactionState
}

// New builds a Lifecycle Manager over the session store it writes and the messenger it uses for agent nudges.
func New(store sessionStore, messenger ports.AgentMessenger) *Manager {
	return NewWithDeps(Deps{Store: store, Messenger: messenger})
}

// NewWithDeps builds a Lifecycle Manager from explicit dependencies.
func NewWithDeps(deps Deps) *Manager {
	return &Manager{store: deps.Store, messenger: deps.Messenger, notifications: deps.Notifications, window: defaultRecentActivityWindow, clock: time.Now, react: newReactionState()}
}

func (m *Manager) mutate(ctx context.Context, id domain.SessionID, fn func(domain.SessionRecord, time.Time) (domain.SessionRecord, bool)) error {
	_, _, err := m.mutateRecord(ctx, id, fn)
	return err
}

func (m *Manager) mutateRecord(ctx context.Context, id domain.SessionID, fn func(domain.SessionRecord, time.Time) (domain.SessionRecord, bool)) (domain.SessionRecord, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil || !ok {
		return domain.SessionRecord{}, false, err
	}
	now := m.clock()
	next, changed := fn(rec, now)
	if !changed {
		return next, false, nil
	}
	next.UpdatedAt = now
	if err := m.store.UpdateSession(ctx, next); err != nil {
		return domain.SessionRecord{}, false, err
	}
	return next, true, nil
}

// ApplyRuntimeObservation only writes when runtime liveness is unambiguous. A
// failed probe or liveness disagreement is ignored; no transient lifecycle state is stored.
func (m *Manager) ApplyRuntimeObservation(ctx context.Context, id domain.SessionID, f ports.RuntimeFacts) error {
	next, changed, err := m.mutateRecord(ctx, id, func(cur domain.SessionRecord, now time.Time) (domain.SessionRecord, bool) {
		if cur.IsTerminated || !runtimeClearlyDead(f, cur.Activity, now, m.window) {
			return cur, false
		}
		next := cur
		next.IsTerminated = true
		next.Activity = domain.Activity{State: domain.ActivityExited, LastActivityAt: timeOr(f.ObservedAt, now)}
		return next, true
	})
	if err != nil || !changed {
		return err
	}
	return m.notify(ctx, domain.NotificationIntent{
		Type:       domain.NotificationSessionExited,
		Priority:   domain.NotificationWarning,
		ProjectID:  next.ProjectID,
		SessionID:  next.ID,
		Source:     "lifecycle.runtime_observation",
		DedupeKey:  "session-exited:" + string(next.ID) + ":" + next.Activity.LastActivityAt.UTC().Format(time.RFC3339Nano),
		OccurredAt: next.Activity.LastActivityAt,
		Context: domain.NotificationIntentContext{
			Reason: "runtime_dead",
			Facts:  map[string]any{"probe": f.Probe},
		},
	})
}

// ApplyActivitySignal records an authoritative agent activity signal.
func (m *Manager) ApplyActivitySignal(ctx context.Context, id domain.SessionID, s ports.ActivitySignal) error {
	if !s.Valid {
		return nil
	}
	next, changed, err := m.mutateRecord(ctx, id, func(cur domain.SessionRecord, now time.Time) (domain.SessionRecord, bool) {
		if cur.IsTerminated {
			return cur, false
		}
		next := cur
		act := domain.Activity{State: s.State, LastActivityAt: timeOr(s.Timestamp, now)}
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
	switch next.Activity.State {
	case domain.ActivityWaitingInput:
		return m.notify(ctx, domain.NotificationIntent{
			Type:       domain.NotificationSessionInput,
			Priority:   domain.NotificationUrgent,
			ProjectID:  next.ProjectID,
			SessionID:  next.ID,
			Source:     "lifecycle.activity_signal",
			DedupeKey:  "session-input:" + string(next.ID) + ":" + next.Activity.LastActivityAt.UTC().Format(time.RFC3339Nano),
			OccurredAt: next.Activity.LastActivityAt,
			Context:    domain.NotificationIntentContext{Reason: "waiting_input"},
		})
	case domain.ActivityExited:
		return m.notify(ctx, domain.NotificationIntent{
			Type:       domain.NotificationSessionExited,
			Priority:   domain.NotificationWarning,
			ProjectID:  next.ProjectID,
			SessionID:  next.ID,
			Source:     "lifecycle.activity_signal",
			DedupeKey:  "session-exited:" + string(next.ID) + ":" + next.Activity.LastActivityAt.UTC().Format(time.RFC3339Nano),
			OccurredAt: next.Activity.LastActivityAt,
			Context:    domain.NotificationIntentContext{Reason: "activity_exited"},
		})
	default:
		return nil
	}
}

// MarkSpawned marks a newly spawned or restored session live and stores runtime/workspace handles.
func (m *Manager) MarkSpawned(ctx context.Context, id domain.SessionID, metadata domain.SessionMetadata) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("lifecycle: MarkSpawned for unknown session %q", id)
	}
	now := m.clock()
	rec.IsTerminated = false
	rec.Activity = domain.Activity{State: domain.ActivityIdle, LastActivityAt: now}
	rec.Metadata = mergeMetadata(rec.Metadata, metadata)
	rec.UpdatedAt = now
	return m.store.UpdateSession(ctx, rec)
}

// MarkTerminated marks a session terminated without tearing down external resources.
func (m *Manager) MarkTerminated(ctx context.Context, id domain.SessionID) error {
	return m.mutate(ctx, id, func(cur domain.SessionRecord, now time.Time) (domain.SessionRecord, bool) {
		if cur.IsTerminated {
			return cur, false
		}
		cur.IsTerminated = true
		cur.Activity = domain.Activity{State: domain.ActivityExited, LastActivityAt: now}
		return cur, true
	})
}

func (m *Manager) notify(ctx context.Context, intent domain.NotificationIntent) error {
	if m.notifications == nil {
		return nil
	}
	return m.notifications.Notify(ctx, intent)
}

// sameActivity reports whether two activity signals describe the same state.
// LastActivityAt is intentionally ignored: same-state repeats (e.g. a stream
// of idle notifications) must not rewrite UpdatedAt or fan out a CDC event.
// LastActivityAt now marks when this state was first entered since the last
// transition, which is the timestamp a UI actually wants.
func sameActivity(a, b domain.Activity) bool {
	return a.State == b.State
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
