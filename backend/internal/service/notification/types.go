// Package notification enriches lifecycle notification intents, persists unread
// rows, and dispatches created notifications to dashboard-facing publishers.
package notification

import (
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Intent is the service boundary shape. It aliases the lifecycle contract so
// Manager.Notify also satisfies lifecycle's NotificationSink without copying.
type Intent = ports.NotificationIntent

// TargetKind describes what a dashboard should navigate to for a notification.
type TargetKind string

const (
	// TargetSession navigates to a session detail view.
	TargetSession TargetKind = "session"
	// TargetPR navigates to a pull request view.
	TargetPR TargetKind = "pr"
)

// Target is the service-facing navigation metadata for a notification.
type Target struct {
	Kind      TargetKind
	SessionID domain.SessionID
	PRURL     string
}

// Notification is the dashboard-facing service DTO assembled from a stored row.
type Notification struct {
	domain.NotificationRecord
	Target Target
}

// ListFilter controls unread notification listing.
type ListFilter struct {
	ProjectID domain.ProjectID
	Limit     int
}
