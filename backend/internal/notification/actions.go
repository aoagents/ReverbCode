package notification

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const (
	ActionOpenSession    = "open-session"
	ActionOpenPR         = "open-pr"
	ActionOpenReview     = "open-review"
	ActionOpenCI         = "open-ci"
	ActionRestoreSession = "restore-session"
	ActionSendMessage    = "send-message"
	ActionMergePR        = "merge-pr"
	ActionMarkRead       = "mark-read"
	ActionDismiss        = "dismiss"
)

var allowedActionKinds = map[string]struct{}{
	ActionOpenSession:    {},
	ActionOpenPR:         {},
	ActionOpenReview:     {},
	ActionOpenCI:         {},
	ActionRestoreSession: {},
	ActionSendMessage:    {},
	ActionMergePR:        {},
	ActionMarkRead:       {},
	ActionDismiss:        {},
}

var allowedExternalHosts = []string{"github.com", "gitlab.com", "linear.app"}

// ActionStore is the minimal durable surface needed by notification actions.
type ActionStore interface {
	GetNotification(ctx context.Context, id string) (domain.Notification, bool, error)
	MarkNotificationRead(ctx context.Context, id string, at time.Time) (domain.Notification, bool, error)
	ArchiveNotification(ctx context.Context, id string, at time.Time) (domain.Notification, bool, error)
}

type ActionError struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *ActionError) Error() string { return e.Message }

type ActionResult struct {
	OK       bool           `json:"ok"`
	ActionID string         `json:"actionId"`
	Kind     string         `json:"kind"`
	Result   map[string]any `json:"result,omitempty"`
}

// ExecuteAction validates and executes a user-visible notification action. It
// deliberately supports only an allowlist of local/navigation actions; payloads
// never supply arbitrary callback endpoints or HTTP methods.
func ExecuteAction(ctx context.Context, store ActionStore, notificationID, actionID string, now time.Time) (ActionResult, error) {
	if store == nil {
		return ActionResult{}, &ActionError{StatusCode: 500, Code: "NOTIFICATION_STORE_UNAVAILABLE", Message: "Notification store unavailable"}
	}
	row, ok, err := store.GetNotification(ctx, notificationID)
	if err != nil {
		return ActionResult{}, err
	}
	if !ok {
		return ActionResult{}, &ActionError{StatusCode: 404, Code: "NOTIFICATION_NOT_FOUND", Message: "Notification not found"}
	}
	action, ok := FindAction(row, actionID)
	if !ok {
		return ActionResult{}, &ActionError{StatusCode: 404, Code: "ACTION_NOT_FOUND", Message: "Notification action not found"}
	}
	action = NormalizeActionKind(action)
	if err := ValidateAction(row, action); err != nil {
		return ActionResult{}, err
	}
	kind := action.Kind
	switch kind {
	case ActionOpenSession, ActionRestoreSession, ActionSendMessage:
		route := action.Route
		if route == "" {
			route = SessionRoute(row)
		}
		return ActionResult{OK: true, ActionID: action.ID, Kind: kind, Result: map[string]any{"route": route}}, nil
	case ActionOpenPR, ActionOpenReview, ActionOpenCI:
		target := action.URL
		if target == "" {
			target = URLFromPayload(row.Payload)
		}
		if err := ValidateExternalURL(target); err != nil {
			return ActionResult{}, err
		}
		return ActionResult{OK: true, ActionID: action.ID, Kind: kind, Result: map[string]any{"url": target}}, nil
	case ActionMarkRead:
		updated, _, err := store.MarkNotificationRead(ctx, string(row.ID), now)
		if err != nil {
			return ActionResult{}, err
		}
		return ActionResult{OK: true, ActionID: action.ID, Kind: kind, Result: map[string]any{"readAt": timeOrNil(updated.ReadAt)}}, nil
	case ActionDismiss:
		updated, _, err := store.ArchiveNotification(ctx, string(row.ID), now)
		if err != nil {
			return ActionResult{}, err
		}
		return ActionResult{OK: true, ActionID: action.ID, Kind: kind, Result: map[string]any{"archivedAt": timeOrNil(updated.ArchivedAt)}}, nil
	case ActionMergePR:
		return ActionResult{}, &ActionError{StatusCode: 409, Code: "ACTION_PRECONDITION_FAILED", Message: "PR merge preconditions are not currently satisfied"}
	default:
		return ActionResult{}, &ActionError{StatusCode: 403, Code: "ACTION_NOT_ALLOWED", Message: "Notification action is not allowed"}
	}
}

func FindAction(row domain.Notification, actionID string) (domain.NotificationAction, bool) {
	for _, action := range row.Actions {
		if action.ID == actionID {
			return action, true
		}
	}
	return domain.NotificationAction{}, false
}

// NormalizeActionKind accepts the legacy route/url storage kind for old rows
// but maps it through the action id before validation. New rows should persist
// one of the public allowlisted kinds directly.
func NormalizeActionKind(action domain.NotificationAction) domain.NotificationAction {
	switch action.Kind {
	case "", "route", "url":
		action.Kind = action.ID
	}
	return action
}

func ValidateAction(row domain.Notification, action domain.NotificationAction) error {
	action = NormalizeActionKind(action)
	if _, ok := allowedActionKinds[action.Kind]; !ok {
		return &ActionError{StatusCode: 403, Code: "ACTION_NOT_ALLOWED", Message: "Notification action is not allowed"}
	}
	if strings.TrimSpace(action.Method) != "" {
		return &ActionError{StatusCode: 403, Code: "ACTION_NOT_ALLOWED", Message: "Notification actions cannot define arbitrary methods or callbacks"}
	}
	if action.Route != "" {
		if err := ValidateInternalRoute(action.Route); err != nil {
			return err
		}
	}
	if action.URL != "" {
		if err := ValidateExternalURL(action.URL); err != nil {
			return err
		}
	}
	if action.Kind == ActionOpenSession && action.Route == "" && (row.ProjectID == "" || row.SessionID == "") {
		return &ActionError{StatusCode: 422, Code: "ACTION_TARGET_INVALID", Message: "Open session action is missing a target"}
	}
	return nil
}

func ValidateInternalRoute(route string) error {
	if route == "" || !strings.HasPrefix(route, "/") || strings.HasPrefix(route, "//") || strings.Contains(route, "\\") {
		return &ActionError{StatusCode: 422, Code: "ACTION_TARGET_INVALID", Message: "Notification action route is invalid"}
	}
	if strings.ContainsAny(route, "\x00\r\n\t") {
		return &ActionError{StatusCode: 422, Code: "ACTION_TARGET_INVALID", Message: "Notification action route is invalid"}
	}
	u, err := url.Parse(route)
	if err != nil || u.IsAbs() || u.Host != "" {
		return &ActionError{StatusCode: 422, Code: "ACTION_TARGET_INVALID", Message: "Notification action route is invalid"}
	}
	return nil
}

func ValidateExternalURL(raw string) error {
	if raw == "" {
		return &ActionError{StatusCode: 422, Code: "ACTION_TARGET_INVALID", Message: "Notification action URL is missing"}
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" {
		return &ActionError{StatusCode: 422, Code: "ACTION_TARGET_INVALID", Message: "Notification action URL must be HTTPS"}
	}
	host := strings.ToLower(u.Hostname())
	for _, allowed := range allowedExternalHosts {
		if host == allowed || strings.HasSuffix(host, "."+allowed) {
			return nil
		}
	}
	return &ActionError{StatusCode: 403, Code: "ACTION_NOT_ALLOWED", Message: "Notification action URL host is not allowed"}
}

func SessionRoute(row domain.Notification) string {
	return fmt.Sprintf("/projects/%s/sessions/%s", row.ProjectID, row.SessionID)
}

func URLFromPayload(raw json.RawMessage) string {
	var p struct {
		Subject struct {
			PR struct {
				URL string `json:"url"`
			} `json:"pr"`
		} `json:"subject"`
		PR struct {
			URL string `json:"url"`
		} `json:"pr"`
		URL string `json:"url"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return ""
	}
	switch {
	case p.Subject.PR.URL != "":
		return p.Subject.PR.URL
	case p.PR.URL != "":
		return p.PR.URL
	default:
		return p.URL
	}
}

func timeOrNil(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC()
}

func IsActionError(err error) (*ActionError, bool) {
	var ae *ActionError
	ok := errors.As(err, &ae)
	return ae, ok
}
