package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// NotificationRow is the storage-facing notification row. It aliases the
// provider-neutral domain type so callers do not depend on sqlc structs.
type NotificationRow = domain.Notification

// NotificationFilter constrains ListNotifications. A zero filter returns the
// newest notifications across projects.
type NotificationFilter struct {
	ProjectID  string
	SessionID  string
	UnreadOnly bool
	Limit      int
}

const defaultNotificationLimit = 100

// EnqueueNotification inserts a notification exactly once per dedupe key. The
// returned bool is true when a new row was created; false means the existing row
// for the same dedupe key was returned.
func (s *Store) EnqueueNotification(ctx context.Context, row NotificationRow) (NotificationRow, bool, error) {
	row = normalizeNotification(row)
	actionsJSON, err := json.Marshal(row.Actions)
	if err != nil {
		return NotificationRow{}, false, fmt.Errorf("marshal notification actions: %w", err)
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	got, err := s.qw.InsertNotification(ctx, gen.InsertNotificationParams{
		ProjectID:    row.ProjectID,
		SessionID:    row.SessionID,
		Source:       row.Source,
		EventType:    row.EventType,
		SemanticType: row.SemanticType,
		Priority:     row.Priority,
		Message:      row.Message,
		PayloadJson:  string(row.Payload),
		ActionsJson:  string(actionsJSON),
		DedupeKey:    row.DedupeKey,
		CauseKey:     row.CauseKey,
		CreatedAt:    row.CreatedAt,
		UpdatedAt:    row.UpdatedAt,
	})
	if errors.Is(err, sql.ErrNoRows) {
		existing, readErr := s.qw.GetNotificationByDedupeKey(ctx, row.DedupeKey)
		if readErr != nil {
			return NotificationRow{}, false, fmt.Errorf("get notification by dedupe %q: %w", row.DedupeKey, readErr)
		}
		mapped, mapErr := notificationFromGen(existing)
		return mapped, false, mapErr
	}
	if err != nil {
		return NotificationRow{}, false, fmt.Errorf("insert notification: %w", err)
	}
	mapped, err := notificationFromGen(got)
	return mapped, true, err
}

// GetNotification returns one notification by id, or ok=false if absent.
func (s *Store) GetNotification(ctx context.Context, id string) (NotificationRow, bool, error) {
	row, err := s.qr.GetNotification(ctx, domain.NotificationID(id))
	if errors.Is(err, sql.ErrNoRows) {
		return NotificationRow{}, false, nil
	}
	if err != nil {
		return NotificationRow{}, false, fmt.Errorf("get notification %s: %w", id, err)
	}
	mapped, err := notificationFromGen(row)
	return mapped, true, err
}

// ListNotifications returns notifications in descending seq order.
func (s *Store) ListNotifications(ctx context.Context, filter NotificationFilter) ([]NotificationRow, error) {
	limit := int64(filter.Limit)
	if limit <= 0 {
		limit = defaultNotificationLimit
	}

	var (
		rows []gen.Notification
		err  error
	)
	switch {
	case filter.UnreadOnly:
		rows, err = s.qr.ListUnreadNotifications(ctx, limit)
	case filter.SessionID != "":
		rows, err = s.qr.ListNotificationsBySession(ctx, gen.ListNotificationsBySessionParams{SessionID: domain.SessionID(filter.SessionID), Limit: limit})
	case filter.ProjectID != "":
		rows, err = s.qr.ListNotificationsByProject(ctx, gen.ListNotificationsByProjectParams{ProjectID: domain.ProjectID(filter.ProjectID), Limit: limit})
	default:
		rows, err = s.qr.ListNotifications(ctx, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("list notifications: %w", err)
	}
	return notificationsFromGen(rows)
}

// MarkNotificationRead marks an unread notification read. The returned bool is
// true only when the row changed; repeated calls return the existing row with
// changed=false and emit no CDC update.
func (s *Store) MarkNotificationRead(ctx context.Context, id string, at time.Time) (NotificationRow, bool, error) {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	row, err := s.qw.MarkNotificationRead(ctx, gen.MarkNotificationReadParams{
		ReadAt:    sql.NullTime{Time: at, Valid: true},
		UpdatedAt: at,
		ID:        domain.NotificationID(id),
	})
	return s.changedNotificationResult(ctx, row, id, true, err)
}

// MarkNotificationUnread clears read_at. Repeated calls are idempotent and emit
// no CDC update.
func (s *Store) MarkNotificationUnread(ctx context.Context, id string) (NotificationRow, bool, error) {
	now := time.Now().UTC()
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	row, err := s.qw.MarkNotificationUnread(ctx, gen.MarkNotificationUnreadParams{UpdatedAt: now, ID: domain.NotificationID(id)})
	return s.changedNotificationResult(ctx, row, id, true, err)
}

// ArchiveNotification marks a notification archived. Repeated calls are
// idempotent and emit no CDC update.
func (s *Store) ArchiveNotification(ctx context.Context, id string, at time.Time) (NotificationRow, bool, error) {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	row, err := s.qw.ArchiveNotification(ctx, gen.ArchiveNotificationParams{
		ArchivedAt: sql.NullTime{Time: at, Valid: true},
		UpdatedAt:  at,
		ID:         domain.NotificationID(id),
	})
	return s.changedNotificationResult(ctx, row, id, true, err)
}

func (s *Store) changedNotificationResult(ctx context.Context, row gen.Notification, id string, changed bool, err error) (NotificationRow, bool, error) {
	if errors.Is(err, sql.ErrNoRows) {
		existing, readErr := s.qw.GetNotification(ctx, domain.NotificationID(id))
		if errors.Is(readErr, sql.ErrNoRows) {
			return NotificationRow{}, false, nil
		}
		if readErr != nil {
			return NotificationRow{}, false, fmt.Errorf("get notification %s: %w", id, readErr)
		}
		mapped, mapErr := notificationFromGen(existing)
		return mapped, false, mapErr
	}
	if err != nil {
		return NotificationRow{}, false, err
	}
	mapped, mapErr := notificationFromGen(row)
	return mapped, changed, mapErr
}

func normalizeNotification(row NotificationRow) NotificationRow {
	now := time.Now().UTC()
	if row.Source == "" {
		row.Source = domain.NotificationSourceLifecycle
	}
	if len(row.Payload) == 0 {
		row.Payload = json.RawMessage(`{}`)
	}
	if row.Actions == nil {
		row.Actions = []domain.NotificationAction{}
	}
	if row.CreatedAt.IsZero() {
		row.CreatedAt = now
	}
	if row.UpdatedAt.IsZero() {
		row.UpdatedAt = row.CreatedAt
	}
	return row
}

func notificationsFromGen(rows []gen.Notification) ([]NotificationRow, error) {
	out := make([]NotificationRow, 0, len(rows))
	for _, r := range rows {
		row, err := notificationFromGen(r)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, nil
}

func notificationFromGen(r gen.Notification) (NotificationRow, error) {
	var actions []domain.NotificationAction
	if r.ActionsJson == "" {
		r.ActionsJson = "[]"
	}
	if err := json.Unmarshal([]byte(r.ActionsJson), &actions); err != nil {
		return NotificationRow{}, fmt.Errorf("decode notification actions %s: %w", r.ID, err)
	}
	row := NotificationRow{
		Seq:          r.Seq,
		ID:           r.ID,
		ProjectID:    r.ProjectID,
		SessionID:    r.SessionID,
		Source:       r.Source,
		EventType:    r.EventType,
		SemanticType: r.SemanticType,
		Priority:     r.Priority,
		Message:      r.Message,
		Payload:      append(json.RawMessage(nil), []byte(r.PayloadJson)...),
		Actions:      actions,
		DedupeKey:    r.DedupeKey,
		CauseKey:     r.CauseKey,
		CreatedAt:    r.CreatedAt,
		UpdatedAt:    r.UpdatedAt,
	}
	if r.ReadAt.Valid {
		row.ReadAt = r.ReadAt.Time
	}
	if r.ArchivedAt.Valid {
		row.ArchivedAt = r.ArchivedAt.Time
	}
	return row, nil
}
