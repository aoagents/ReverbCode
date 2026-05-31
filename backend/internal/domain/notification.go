package domain

import (
	"encoding/json"
	"time"
)

// NotificationID is the stable public identifier for a persisted notification.
type NotificationID string

// NotificationSource identifies the subsystem that produced a durable
// notification.
type NotificationSource string

// Notification sources.
const (
	NotificationSourceLifecycle NotificationSource = "lifecycle"
)

// NotificationEventType is the internal event that produced a durable
// notification.
type NotificationEventType string

// NotificationSemanticType is the stable public category consumed by clients.
type NotificationSemanticType string

// NotificationPriority ranks urgency for human-facing notification delivery.
type NotificationPriority string

// Notification priorities, highest urgency first.
const (
	NotificationPriorityUrgent  NotificationPriority = "urgent"
	NotificationPriorityAction  NotificationPriority = "action"
	NotificationPriorityWarning NotificationPriority = "warning"
	NotificationPriorityInfo    NotificationPriority = "info"
)

// Notification is the provider-neutral durable notification read model. It is
// sink-agnostic: desktop, dashboard, Slack, webhooks, etc. all consume the same
// semantic payload and actions later.
type Notification struct {
	Seq          int64
	ID           NotificationID
	ProjectID    ProjectID
	SessionID    SessionID
	Source       NotificationSource
	EventType    NotificationEventType
	SemanticType NotificationSemanticType
	Priority     NotificationPriority
	Message      string
	Payload      json.RawMessage
	Actions      []NotificationAction
	DedupeKey    string
	CauseKey     string
	ReadAt       time.Time
	ArchivedAt   time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// NotificationAction is a provider-neutral action descriptor. Renderers may use
// Route for app-local navigation, URL for external navigation, or Method for a
// future command/action endpoint.
type NotificationAction struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Label  string `json:"label"`
	Route  string `json:"route,omitempty"`
	URL    string `json:"url,omitempty"`
	Method string `json:"method,omitempty"`
}
