package notification

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const (
	// DefaultListLimit is the unread notification page size used when none is requested.
	DefaultListLimit = 50
	// MaxListLimit caps unread notification API responses.
	MaxListLimit = 100
)

// Manager validates lifecycle intents, enriches them into stored rows, and persists
// unread notifications.
type Manager struct {
	store Store
	clock func() time.Time
	newID func() string
}

// Deps configures a Manager.
type Deps struct {
	Store Store
	Clock func() time.Time
	NewID func() string
}

// New constructs a Manager with production defaults for optional collaborators.
func New(d Deps) *Manager {
	m := &Manager{store: d.Store, clock: d.Clock, newID: d.NewID}
	if m.clock == nil {
		m.clock = time.Now
	}
	if m.newID == nil {
		m.newID = func() string { return "ntf_" + uuid.NewString() }
	}
	return m
}

// Notify stores one notification intent. Duplicate unread rows are treated as a clean no-op.
func (m *Manager) Notify(ctx context.Context, intent Intent) error {
	if m == nil || m.store == nil {
		return errors.New("notification: store is required")
	}
	if intent.CreatedAt.IsZero() {
		intent.CreatedAt = m.clock().UTC()
	}
	rec, err := enrich(intent)
	if err != nil {
		return fmt.Errorf("notification enrich: %w", err)
	}
	rec.ID = m.newID()
	_, inserted, err := m.store.CreateNotification(ctx, rec)
	if err != nil {
		return fmt.Errorf("notification store: %w", err)
	}
	if !inserted {
		return nil
	}
	return nil
}

// ListUnread returns unread notifications newest-first.
func (m *Manager) ListUnread(ctx context.Context, filter ListFilter) ([]Notification, error) {
	if m == nil || m.store == nil {
		return nil, errors.New("notification: store is required")
	}
	limit := normalizeLimit(filter.Limit)
	rows, err := m.store.ListUnreadNotifications(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]Notification, 0, len(rows))
	for _, row := range rows {
		out = append(out, notificationFromRecord(row))
	}
	return out, nil
}

func normalizeLimit(limit int) int {
	if limit <= 0 {
		return DefaultListLimit
	}
	if limit > MaxListLimit {
		return MaxListLimit
	}
	return limit
}

func notificationFromRecord(rec domain.NotificationRecord) Notification {
	return Notification{NotificationRecord: rec, Target: targetForRecord(rec)}
}

func targetForRecord(rec domain.NotificationRecord) Target {
	if rec.PRURL != "" {
		return Target{Kind: TargetPR, SessionID: rec.SessionID, PRURL: rec.PRURL}
	}
	return Target{Kind: TargetSession, SessionID: rec.SessionID}
}
