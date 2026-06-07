package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// NotificationID identifies one persisted canonical notification.
type NotificationID string

// NotificationType identifies the product-level event a notification represents.
type NotificationType string

// NotificationPriority controls the user-visible urgency of a notification.
type NotificationPriority string

// NotificationStatus is the persisted lifecycle state of a notification.
type NotificationStatus string

// Notification types emitted by lifecycle and persisted by storage.
const (
	NotificationCIFailing      NotificationType = "ci.failing"
	NotificationReviewChanges  NotificationType = "review.changes_requested"
	NotificationMergeConflicts NotificationType = "merge.conflicts"
	NotificationMergeReady     NotificationType = "merge.ready"
	NotificationMergeCompleted NotificationType = "merge.completed"
	NotificationSessionInput   NotificationType = "session.needs_input"
	NotificationSessionExited  NotificationType = "session.exited"
)

// Notification priorities persisted by storage.
const (
	NotificationUrgent         NotificationPriority = "urgent"
	NotificationPriorityAction NotificationPriority = "action"
	NotificationWarning        NotificationPriority = "warning"
	NotificationInfo           NotificationPriority = "info"
)

// Notification statuses persisted by storage.
const (
	NotificationUnread    NotificationStatus = "unread"
	NotificationRead      NotificationStatus = "read"
	NotificationDismissed NotificationStatus = "dismissed"
	NotificationResolved  NotificationStatus = "resolved"
)

// NotificationIntent is the lifecycle-to-notification contract. Lifecycle owns
// the relevance decision; the notification service owns enrichment, copy,
// semantic actions, dedupe, and persistence.
type NotificationIntent struct {
	Type       NotificationType
	Priority   NotificationPriority
	ProjectID  ProjectID
	SessionID  SessionID
	Source     string
	DedupeKey  string
	OccurredAt time.Time
	Context    NotificationIntentContext
}

// NotificationIntentContext carries small, stable fragments lifecycle already
// has at the decision point. It intentionally excludes raw provider payloads and
// channel/dashboard rendering details.
type NotificationIntentContext struct {
	PRURL      string         `json:"prUrl,omitempty"`
	CheckName  string         `json:"checkName,omitempty"`
	CheckURL   string         `json:"checkUrl,omitempty"`
	CommitHash string         `json:"commitHash,omitempty"`
	ReviewIDs  []string       `json:"reviewIds,omitempty"`
	ThreadIDs  []string       `json:"threadIds,omitempty"`
	MergeState string         `json:"mergeState,omitempty"`
	Reason     string         `json:"reason,omitempty"`
	Facts      map[string]any `json:"facts,omitempty"`
}

// Validate rejects malformed notification intents before the service performs
// any enrichment or persistence work.
func (i NotificationIntent) Validate() error {
	if i.Type == "" {
		return errors.New("notification intent: missing type")
	}
	if !validNotificationType(i.Type) {
		return fmt.Errorf("notification intent: unsupported type %q", i.Type)
	}
	if i.Priority == "" {
		return errors.New("notification intent: missing priority")
	}
	if !validNotificationPriority(i.Priority) {
		return fmt.Errorf("notification intent: unsupported priority %q", i.Priority)
	}
	if i.ProjectID == "" {
		return errors.New("notification intent: missing project id")
	}
	if i.SessionID == "" {
		return errors.New("notification intent: missing session id")
	}
	if i.Source == "" {
		return errors.New("notification intent: missing source")
	}
	if i.DedupeKey == "" {
		return errors.New("notification intent: missing dedupe key")
	}
	if err := ensureJSONSafe(i.Context); err != nil {
		return fmt.Errorf("notification intent: context is not JSON-safe: %w", err)
	}
	return nil
}

func validNotificationType(v NotificationType) bool {
	switch v {
	case NotificationCIFailing, NotificationReviewChanges, NotificationMergeConflicts, NotificationMergeReady,
		NotificationMergeCompleted, NotificationSessionInput, NotificationSessionExited:
		return true
	default:
		return false
	}
}

func validNotificationPriority(v NotificationPriority) bool {
	switch v {
	case NotificationUrgent, NotificationPriorityAction, NotificationWarning, NotificationInfo:
		return true
	default:
		return false
	}
}

func validNotificationStatus(v NotificationStatus) bool {
	switch v {
	case NotificationUnread, NotificationRead, NotificationDismissed, NotificationResolved:
		return true
	default:
		return false
	}
}

// Notification is the canonical persisted notification row. Every notification
// is session-scoped; project-level views should group these session-owned rows.
// It stores concise visible copy plus structured evidence and semantic action
// descriptors.
type Notification struct {
	ID          NotificationID
	Type        NotificationType
	Priority    NotificationPriority
	Status      NotificationStatus
	ProjectID   ProjectID
	SessionID   SessionID
	Source      string
	DedupeKey   string
	Fingerprint string
	Title       string
	Summary     string
	Body        string
	Subject     NotificationSubject
	Data        map[string]any
	Actions     []NotificationAction
	OccurredAt  time.Time
	ReadAt      *time.Time
	DismissedAt *time.Time
	ResolvedAt  *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// NotificationSubject describes what the notification is about without tying
// consumers to an API route or dashboard URL shape.
type NotificationSubject struct {
	Kind        string    `json:"kind,omitempty"`
	Label       string    `json:"label,omitempty"`
	ProjectID   ProjectID `json:"projectId,omitempty"`
	SessionID   SessionID `json:"sessionId,omitempty"`
	PRURL       string    `json:"prUrl,omitempty"`
	PRNumber    int       `json:"prNumber,omitempty"`
	PRTitle     string    `json:"prTitle,omitempty"`
	ProjectName string    `json:"projectName,omitempty"`
}

// NotificationAction is a semantic descriptor. Execution of actions is a future
// API/dashboard concern; this backend scope only stores the descriptors.
type NotificationAction struct {
	ID      string         `json:"id"`
	Label   string         `json:"label"`
	Kind    string         `json:"kind"` // route, link, command, callback
	URL     string         `json:"url,omitempty"`
	Route   string         `json:"route,omitempty"`
	Payload map[string]any `json:"payload,omitempty"`
	Primary bool           `json:"primary,omitempty"`
}

// Validate checks that an action descriptor can be safely persisted as JSON.
func (a NotificationAction) Validate() error {
	if a.ID == "" {
		return errors.New("missing id")
	}
	if a.Label == "" {
		return errors.New("missing label")
	}
	if a.Kind == "" {
		return errors.New("missing kind")
	}
	return ensureJSONSafe(a)
}

// NotificationContent is channel-neutral canonical copy produced by the central
// notification maker.
type NotificationContent struct {
	Title   string
	Summary string
	Body    string
}

// NotificationResolveFilter selects earlier notifications that are no longer
// relevant after a later lifecycle fact, such as a merged PR.
type NotificationResolveFilter struct {
	ProjectID         ProjectID
	SessionID         *SessionID
	PRURL             string
	Types             []NotificationType
	DedupeKeyPrefixes []string
	Statuses          []NotificationStatus
}

// Normalize fills storage defaults that are meaningful at the domain boundary.
func (n Notification) Normalize() Notification {
	if n.Status == "" {
		n.Status = NotificationUnread
	}
	if n.Data == nil {
		n.Data = map[string]any{}
	}
	if n.Actions == nil {
		n.Actions = []NotificationAction{}
	}
	return n
}

// Validate rejects invalid persisted notification rows before storage mapping.
func (n Notification) Validate() error {
	n = n.Normalize()
	if n.ID == "" {
		return errors.New("notification: missing id")
	}
	if n.Type == "" || !validNotificationType(n.Type) {
		return fmt.Errorf("notification: invalid type %q", n.Type)
	}
	if n.Priority == "" || !validNotificationPriority(n.Priority) {
		return fmt.Errorf("notification: invalid priority %q", n.Priority)
	}
	if n.Status == "" || !validNotificationStatus(n.Status) {
		return fmt.Errorf("notification: invalid status %q", n.Status)
	}
	if n.ProjectID == "" {
		return errors.New("notification: missing project id")
	}
	if n.SessionID == "" {
		return errors.New("notification: missing session id")
	}
	if n.Source == "" {
		return errors.New("notification: missing source")
	}
	if n.DedupeKey == "" {
		return errors.New("notification: missing dedupe key")
	}
	if n.Fingerprint == "" {
		return errors.New("notification: missing fingerprint")
	}
	if n.Title == "" {
		return errors.New("notification: missing title")
	}
	if n.Summary == "" {
		return errors.New("notification: missing summary")
	}
	if n.OccurredAt.IsZero() || n.CreatedAt.IsZero() || n.UpdatedAt.IsZero() {
		return errors.New("notification: missing timestamps")
	}
	for _, a := range n.Actions {
		if err := a.Validate(); err != nil {
			return fmt.Errorf("notification: action %q: %w", a.ID, err)
		}
	}
	if err := ensureJSONSafe(n.Subject); err != nil {
		return fmt.Errorf("notification: subject is not JSON-safe: %w", err)
	}
	if err := ensureJSONSafe(n.Data); err != nil {
		return fmt.Errorf("notification: data is not JSON-safe: %w", err)
	}
	return nil
}

func ensureJSONSafe(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	var decoded any
	return json.Unmarshal(b, &decoded)
}
