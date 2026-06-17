package notification

import (
	"context"
	"errors"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const (
	// DefaultListLimit is the unread notification page size used when none is requested.
	DefaultListLimit = 50
	// MaxListLimit caps unread notification API responses.
	MaxListLimit = 100
)

// Manager reads stored notifications for REST controllers.
type Manager struct {
	store Store
}

// Deps configures a Manager.
type Deps struct {
	Store Store
}

// New constructs a read-only notification Manager.
func New(d Deps) *Manager {
	return &Manager{store: d.Store}
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
