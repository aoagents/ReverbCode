package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/notification"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

const (
	defaultNotificationAPILimit = 50
	maxNotificationAPILimit     = 500
)

// NotificationStore is the HTTP API's durable notification dependency.
// *sqlite.Store satisfies it.
type NotificationStore interface {
	ListNotifications(ctx context.Context, filter sqlite.NotificationFilter) ([]domain.Notification, error)
	CountUnreadNotifications(ctx context.Context, filter sqlite.NotificationFilter) (int, error)
	GetNotification(ctx context.Context, id string) (domain.Notification, bool, error)
	MarkNotificationRead(ctx context.Context, id string, at time.Time) (domain.Notification, bool, error)
	MarkNotificationUnread(ctx context.Context, id string) (domain.Notification, bool, error)
	ArchiveNotification(ctx context.Context, id string, at time.Time) (domain.Notification, bool, error)
	MarkAllNotificationsRead(ctx context.Context, filter sqlite.NotificationFilter, at time.Time) (int, error)
}

type NotificationsController struct {
	Store       NotificationStore
	ActionToken string
	Clock       func() time.Time
}

func (c *NotificationsController) Register(r chi.Router) {
	r.Get("/notifications", c.list)
	r.Post("/notifications/read-all", c.readAll)
	r.Get("/notifications/{id}", c.get)
	r.Patch("/notifications/{id}", c.patch)
	r.Post("/notifications/{id}/actions/{actionId}", c.action)
}

func (c *NotificationsController) now() time.Time {
	if c.Clock != nil {
		return c.Clock().UTC()
	}
	return time.Now().UTC()
}

func (c *NotificationsController) list(w http.ResponseWriter, r *http.Request) {
	if c.Store == nil {
		unavailable(w, r)
		return
	}
	filter, ok := notificationFilterFromQuery(w, r)
	if !ok {
		return
	}
	rows, err := c.Store.ListNotifications(r.Context(), filter)
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "NOTIFICATIONS_LIST_FAILED", "Failed to list notifications", nil)
		return
	}
	unread, err := c.Store.CountUnreadNotifications(r.Context(), filter)
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "NOTIFICATIONS_COUNT_FAILED", "Failed to count unread notifications", nil)
		return
	}
	records := make([]notificationRecord, 0, len(rows))
	for _, row := range rows {
		records = append(records, notificationRecordFromDomain(row))
	}
	var nextBeforeSeq any
	if len(records) == filter.Limit {
		nextBeforeSeq = records[len(records)-1].Seq
	}
	envelope.WriteJSON(w, http.StatusOK, map[string]any{
		"notifications": records,
		"unreadCount":   unread,
		"limit":         filter.Limit,
		"nextBeforeSeq": nextBeforeSeq,
	})
}

func (c *NotificationsController) get(w http.ResponseWriter, r *http.Request) {
	if c.Store == nil {
		unavailable(w, r)
		return
	}
	row, ok, err := c.Store.GetNotification(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "NOTIFICATION_GET_FAILED", "Failed to get notification", nil)
		return
	}
	if !ok {
		notificationNotFound(w, r)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, map[string]any{"notification": notificationRecordFromDomain(row)})
}

func (c *NotificationsController) patch(w http.ResponseWriter, r *http.Request) {
	if c.Store == nil {
		unavailable(w, r)
		return
	}
	id := chi.URLParam(r, "id")
	var body struct {
		Read     *bool `json:"read"`
		Archived *bool `json:"archived"`
	}
	if err := decodeJSON(r, &body); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	if body.Read == nil && body.Archived == nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_NOTIFICATION_UPDATE", "Patch must include read or archived", nil)
		return
	}

	row, ok, err := c.Store.GetNotification(r.Context(), id)
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "NOTIFICATION_GET_FAILED", "Failed to get notification", nil)
		return
	}
	if !ok {
		notificationNotFound(w, r)
		return
	}
	if body.Archived != nil && !*body.Archived && !row.ArchivedAt.IsZero() {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_NOTIFICATION_UPDATE", "Unarchive is not supported by this API", nil)
		return
	}
	now := c.now()
	if body.Read != nil {
		if *body.Read {
			row, _, err = c.Store.MarkNotificationRead(r.Context(), id, now)
		} else {
			row, _, err = c.Store.MarkNotificationUnread(r.Context(), id)
		}
		if err != nil {
			envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "NOTIFICATION_UPDATE_FAILED", "Failed to update notification", nil)
			return
		}
	}
	if body.Archived != nil {
		if *body.Archived {
			row, _, err = c.Store.ArchiveNotification(r.Context(), id, now)
		}
		if err != nil {
			envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "NOTIFICATION_UPDATE_FAILED", "Failed to update notification", nil)
			return
		}
	}
	envelope.WriteJSON(w, http.StatusOK, map[string]any{"notification": notificationRecordFromDomain(row)})
}

func (c *NotificationsController) readAll(w http.ResponseWriter, r *http.Request) {
	if c.Store == nil {
		unavailable(w, r)
		return
	}
	var body struct {
		ProjectID string `json:"projectId"`
		SessionID string `json:"sessionId"`
	}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
			return
		}
	}
	updated, err := c.Store.MarkAllNotificationsRead(r.Context(), sqlite.NotificationFilter{ProjectID: body.ProjectID, SessionID: body.SessionID}, c.now())
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "NOTIFICATIONS_READ_ALL_FAILED", "Failed to mark notifications read", nil)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, map[string]any{"updated": updated})
}

func (c *NotificationsController) action(w http.ResponseWriter, r *http.Request) {
	if c.Store == nil {
		unavailable(w, r)
		return
	}
	if c.ActionToken == "" || r.Header.Get("X-AO-Action-Token") != c.ActionToken {
		envelope.WriteAPIError(w, r, http.StatusForbidden, "forbidden", "ACTION_NOT_ALLOWED", "Notification action token is invalid", nil)
		return
	}
	if r.Body != nil {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
			return
		}
	}
	result, err := notification.ExecuteAction(r.Context(), c.Store, chi.URLParam(r, "id"), chi.URLParam(r, "actionId"), c.now())
	if err != nil {
		if ae, ok := notification.IsActionError(err); ok {
			kind := "bad_request"
			switch ae.StatusCode {
			case http.StatusForbidden:
				kind = "forbidden"
			case http.StatusNotFound:
				kind = "not_found"
			case http.StatusConflict:
				kind = "conflict"
			case http.StatusUnprocessableEntity:
				kind = "unprocessable_entity"
			case http.StatusInternalServerError:
				kind = "internal"
			}
			envelope.WriteAPIError(w, r, ae.StatusCode, kind, ae.Code, ae.Message, nil)
			return
		}
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "ACTION_FAILED", "Notification action failed", nil)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, result)
}

func notificationFilterFromQuery(w http.ResponseWriter, r *http.Request) (sqlite.NotificationFilter, bool) {
	q := r.URL.Query()
	limit := defaultNotificationAPILimit
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_NOTIFICATION_QUERY", "limit must be a number", nil)
			return sqlite.NotificationFilter{}, false
		}
		limit = n
	}
	if limit < 1 {
		limit = 1
	}
	if limit > maxNotificationAPILimit {
		limit = maxNotificationAPILimit
	}
	beforeSeq := int64(0)
	if raw := q.Get("beforeSeq"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 0 {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_NOTIFICATION_QUERY", "beforeSeq must be a non-negative integer", nil)
			return sqlite.NotificationFilter{}, false
		}
		beforeSeq = n
	}
	unreadOnly, ok := parseBoolQuery(w, r, "unreadOnly")
	if !ok {
		return sqlite.NotificationFilter{}, false
	}
	includeArchived, ok := parseBoolQuery(w, r, "includeArchived")
	if !ok {
		return sqlite.NotificationFilter{}, false
	}
	return sqlite.NotificationFilter{
		ProjectID:       q.Get("projectId"),
		SessionID:       q.Get("sessionId"),
		UnreadOnly:      unreadOnly,
		IncludeArchived: includeArchived,
		Limit:           limit,
		BeforeSeq:       beforeSeq,
	}, true
}

func parseBoolQuery(w http.ResponseWriter, r *http.Request, key string) (bool, bool) {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return false, true
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_NOTIFICATION_QUERY", key+" must be a boolean", nil)
		return false, false
	}
	return v, true
}

func unavailable(w http.ResponseWriter, r *http.Request) {
	envelope.WriteAPIError(w, r, http.StatusServiceUnavailable, "unavailable", "NOTIFICATIONS_UNAVAILABLE", "Notifications are not available", nil)
}

func notificationNotFound(w http.ResponseWriter, r *http.Request) {
	envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "NOTIFICATION_NOT_FOUND", "Notification not found", nil)
}

type notificationRecord struct {
	Seq        int64                       `json:"seq"`
	ID         string                      `json:"id"`
	ReceivedAt time.Time                   `json:"receivedAt"`
	ReadAt     any                         `json:"readAt"`
	ArchivedAt any                         `json:"archivedAt"`
	Event      notificationEvent           `json:"event"`
	Actions    []domain.NotificationAction `json:"actions"`
}

type notificationEvent struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	Priority  string          `json:"priority"`
	SessionID string          `json:"sessionId"`
	ProjectID string          `json:"projectId"`
	Timestamp time.Time       `json:"timestamp"`
	Message   string          `json:"message"`
	Data      json.RawMessage `json:"data"`
}

func notificationRecordFromDomain(row domain.Notification) notificationRecord {
	actions := make([]domain.NotificationAction, 0, len(row.Actions))
	for _, action := range row.Actions {
		action = notification.NormalizeActionKind(action)
		if err := notification.ValidateAction(row, action); err == nil {
			actions = append(actions, action)
		}
	}
	return notificationRecord{
		Seq:        row.Seq,
		ID:         string(row.ID),
		ReceivedAt: row.CreatedAt.UTC(),
		ReadAt:     nullableTime(row.ReadAt),
		ArchivedAt: nullableTime(row.ArchivedAt),
		Event: notificationEvent{
			ID:        string(row.ID),
			Type:      row.SemanticType,
			Priority:  row.Priority,
			SessionID: string(row.SessionID),
			ProjectID: string(row.ProjectID),
			Timestamp: row.CreatedAt.UTC(),
			Message:   row.Message,
			Data:      row.Payload,
		},
		Actions: actions,
	}
}

func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC()
}
