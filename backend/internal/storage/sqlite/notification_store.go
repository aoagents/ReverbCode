package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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
	ProjectID       string
	SessionID       string
	UnreadOnly      bool
	IncludeArchived bool
	Limit           int
	BeforeSeq       int64
}

const defaultNotificationLimit = 100

const notificationSelectColumns = `seq, id, project_id, session_id, source, event_type, semantic_type, priority,
    message, payload_json, actions_json, dedupe_key, cause_key, read_at, archived_at, created_at, updated_at, routed_at`

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
		ProjectID:    string(row.ProjectID),
		SessionID:    string(row.SessionID),
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
	row, err := s.qr.GetNotification(ctx, id)
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

	query, args := buildNotificationListQuery(filter, limit)
	rows, err := s.readDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list notifications: %w", err)
	}
	defer rows.Close()

	out := make([]NotificationRow, 0, limit)
	for rows.Next() {
		row, err := scanNotification(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list notifications: %w", err)
	}
	return out, nil
}

// CountUnreadNotifications returns the current unread badge count. Archived
// notifications never contribute to the badge, even when an API list includes
// archived rows for history/backfill.
func (s *Store) CountUnreadNotifications(ctx context.Context, filter NotificationFilter) (int, error) {
	var (
		clauses = []string{"read_at IS NULL", "archived_at IS NULL"}
		args    []any
	)
	if filter.ProjectID != "" {
		clauses = append(clauses, "project_id = ?")
		args = append(args, filter.ProjectID)
	}
	if filter.SessionID != "" {
		clauses = append(clauses, "session_id = ?")
		args = append(args, filter.SessionID)
	}
	query := "SELECT COUNT(*) FROM notifications WHERE " + strings.Join(clauses, " AND ")
	var count int
	if err := s.readDB.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("count unread notifications: %w", err)
	}
	return count, nil
}

// MarkAllNotificationsRead marks visible unread notifications read, optionally
// scoped by project/session, and returns the number of rows changed. SQLite
// fires the notification_updated CDC trigger once per changed row.
func (s *Store) MarkAllNotificationsRead(ctx context.Context, filter NotificationFilter, at time.Time) (int, error) {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	clauses := []string{"read_at IS NULL", "archived_at IS NULL"}
	args := []any{sql.NullTime{Time: at, Valid: true}, at}
	if filter.ProjectID != "" {
		clauses = append(clauses, "project_id = ?")
		args = append(args, filter.ProjectID)
	}
	if filter.SessionID != "" {
		clauses = append(clauses, "session_id = ?")
		args = append(args, filter.SessionID)
	}
	query := "UPDATE notifications SET read_at = ?, updated_at = ? WHERE " + strings.Join(clauses, " AND ")

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	result, err := s.writeDB.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("mark all notifications read: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("mark all notifications read rows affected: %w", err)
	}
	return int(n), nil
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
		ID:        id,
	})
	return s.changedNotificationResult(ctx, row, id, true, err)
}

// MarkNotificationUnread clears read_at. Repeated calls are idempotent and emit
// no CDC update.
func (s *Store) MarkNotificationUnread(ctx context.Context, id string) (NotificationRow, bool, error) {
	now := time.Now().UTC()
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	row, err := s.qw.MarkNotificationUnread(ctx, gen.MarkNotificationUnreadParams{UpdatedAt: now, ID: id})
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
		ID:         id,
	})
	return s.changedNotificationResult(ctx, row, id, true, err)
}

// UnarchiveNotification clears archived_at. Product flows currently avoid
// surfacing unarchive, but the primitive is useful for idempotent PATCH handling
// and tests; callers can decide whether to expose it.
func (s *Store) UnarchiveNotification(ctx context.Context, id string, at time.Time) (NotificationRow, bool, error) {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	row, err := s.writeDB.QueryContext(ctx, `UPDATE notifications
SET archived_at = NULL, updated_at = ?
WHERE id = ? AND archived_at IS NOT NULL
RETURNING `+notificationSelectColumns, at, id)
	if err != nil {
		return NotificationRow{}, false, err
	}
	defer row.Close()
	if row.Next() {
		got, scanErr := scanNotification(row)
		return got, scanErr == nil, scanErr
	}
	if err := row.Err(); err != nil {
		return NotificationRow{}, false, err
	}
	existing, readErr := s.qw.GetNotification(ctx, id)
	if errors.Is(readErr, sql.ErrNoRows) {
		return NotificationRow{}, false, nil
	}
	if readErr != nil {
		return NotificationRow{}, false, fmt.Errorf("get notification %s: %w", id, readErr)
	}
	mapped, mapErr := notificationFromGen(existing)
	return mapped, false, mapErr
}

func (s *Store) changedNotificationResult(ctx context.Context, row gen.Notification, id string, changed bool, err error) (NotificationRow, bool, error) {
	if errors.Is(err, sql.ErrNoRows) {
		existing, readErr := s.qw.GetNotification(ctx, id)
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
		row.Source = "lifecycle"
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

func buildNotificationListQuery(filter NotificationFilter, limit int64) (string, []any) {
	var (
		clauses []string
		args    []any
	)
	if filter.ProjectID != "" {
		clauses = append(clauses, "project_id = ?")
		args = append(args, filter.ProjectID)
	}
	if filter.SessionID != "" {
		clauses = append(clauses, "session_id = ?")
		args = append(args, filter.SessionID)
	}
	if filter.UnreadOnly {
		clauses = append(clauses, "read_at IS NULL")
	}
	if !filter.IncludeArchived {
		clauses = append(clauses, "archived_at IS NULL")
	}
	if filter.BeforeSeq > 0 {
		clauses = append(clauses, "seq < ?")
		args = append(args, filter.BeforeSeq)
	}
	where := ""
	if len(clauses) > 0 {
		where = " WHERE " + strings.Join(clauses, " AND ")
	}
	args = append(args, limit)
	return "SELECT " + notificationSelectColumns + " FROM notifications" + where + " ORDER BY seq DESC LIMIT ?", args
}

type notificationScanner interface {
	Scan(dest ...any) error
}

func scanNotification(scanner notificationScanner) (NotificationRow, error) {
	var r gen.Notification
	if err := scanner.Scan(
		&r.Seq,
		&r.ID,
		&r.ProjectID,
		&r.SessionID,
		&r.Source,
		&r.EventType,
		&r.SemanticType,
		&r.Priority,
		&r.Message,
		&r.PayloadJson,
		&r.ActionsJson,
		&r.DedupeKey,
		&r.CauseKey,
		&r.ReadAt,
		&r.ArchivedAt,
		&r.CreatedAt,
		&r.UpdatedAt,
		&r.RoutedAt,
	); err != nil {
		return NotificationRow{}, err
	}
	return notificationFromGen(r)
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
		ID:           domain.NotificationID(r.ID),
		ProjectID:    domain.ProjectID(r.ProjectID),
		SessionID:    domain.SessionID(r.SessionID),
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
	if r.RoutedAt.Valid {
		row.RoutedAt = r.RoutedAt.Time
	}
	return row, nil
}
