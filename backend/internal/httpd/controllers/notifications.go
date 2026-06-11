package controllers

import (
	"context"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	notificationsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/notification"
)

// NotificationService is the controller-facing notification service contract.
type NotificationService interface {
	ListUnread(ctx context.Context, filter notificationsvc.ListFilter) ([]notificationsvc.Notification, error)
}

// NotificationsController owns the /notifications routes.
type NotificationsController struct {
	Svc NotificationService
}

// Register mounts notification routes on the supplied router.
func (c *NotificationsController) Register(r chi.Router) {
	r.Get("/notifications", c.list)
}

func (c *NotificationsController) list(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/notifications")
		return
	}
	filter, err := parseNotificationListFilter(r)
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_QUERY", err.Error(), nil)
		return
	}
	notifications, err := c.Svc.ListUnread(r.Context(), filter)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ListNotificationsResponse{Notifications: notificationResponses(notifications)})
}

func parseNotificationListFilter(r *http.Request) (notificationsvc.ListFilter, error) {
	q := r.URL.Query()
	status := q.Get("status")
	if status == "" {
		status = string(domain.NotificationUnread)
	}
	if status != string(domain.NotificationUnread) {
		return notificationsvc.ListFilter{}, errNotificationStatusUnsupported
	}
	limit := notificationsvc.DefaultListLimit
	if raw := q.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			return notificationsvc.ListFilter{}, errNotificationLimitInvalid
		}
		limit = parsed
	}
	if limit > notificationsvc.MaxListLimit {
		limit = notificationsvc.MaxListLimit
	}
	return notificationsvc.ListFilter{ProjectID: domain.ProjectID(q.Get("projectId")), Limit: limit}, nil
}

var (
	errNotificationStatusUnsupported = notificationQueryError("status must be unread")
	errNotificationLimitInvalid      = notificationQueryError("limit must be a positive integer")
)

type notificationQueryError string

func (e notificationQueryError) Error() string { return string(e) }

func notificationResponses(in []notificationsvc.Notification) []NotificationResponse {
	out := make([]NotificationResponse, 0, len(in))
	for _, n := range in {
		out = append(out, NotificationResponse{
			ID:        n.ID,
			SessionID: string(n.SessionID),
			ProjectID: string(n.ProjectID),
			PRURL:     n.PRURL,
			Type:      string(n.Type),
			Title:     n.Title,
			Body:      n.Body,
			Status:    string(n.Status),
			CreatedAt: n.CreatedAt,
			Target: NotificationTarget{
				Kind:      string(n.Target.Kind),
				SessionID: string(n.Target.SessionID),
				PRURL:     n.Target.PRURL,
			},
		})
	}
	return out
}
