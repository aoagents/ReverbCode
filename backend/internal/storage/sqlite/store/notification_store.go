package store

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

// UpsertNotification inserts a new logical notification or updates the existing
// row for (project_id, dedupe_key) when the fingerprint changed. It returns
// changed=false when the stored fingerprint already matches, so callers can
// rely on triggers to suppress duplicate CDC events on daemon restarts.
func (s *Store) UpsertNotification(ctx context.Context, n domain.Notification) (domain.Notification, bool, error) {
	n = n.Normalize()
	if err := n.Validate(); err != nil {
		return domain.Notification{}, false, err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	var out domain.Notification
	var changed bool
	err := s.inTx(ctx, "upsert notification", func(q *gen.Queries) error {
		existing, err := q.GetNotificationByDedupeKey(ctx, gen.GetNotificationByDedupeKeyParams{ProjectID: n.ProjectID, DedupeKey: n.DedupeKey})
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if errors.Is(err, sql.ErrNoRows) {
			params, err := notificationInsertParams(n)
			if err != nil {
				return err
			}
			if err := q.InsertNotification(ctx, params); err != nil {
				return err
			}
			row, err := q.GetNotification(ctx, n.ID)
			if err != nil {
				return err
			}
			out, err = notificationFromGen(row)
			if err != nil {
				return err
			}
			changed = true
			return nil
		}
		cur, err := notificationFromGen(existing)
		if err != nil {
			return err
		}
		if cur.Fingerprint == n.Fingerprint {
			out = cur
			changed = false
			return nil
		}
		n.ID = cur.ID
		n.CreatedAt = cur.CreatedAt
		params, err := notificationUpdateParams(n)
		if err != nil {
			return err
		}
		if err := q.UpdateNotificationContent(ctx, params); err != nil {
			return err
		}
		row, err := q.GetNotification(ctx, cur.ID)
		if err != nil {
			return err
		}
		out, err = notificationFromGen(row)
		if err != nil {
			return err
		}
		changed = true
		return nil
	})
	if err != nil {
		return domain.Notification{}, false, err
	}
	return out, changed, nil
}

// ResolveNotifications marks matching unread/read notifications resolved. The
// UPDATE itself is the only write; notification_updated CDC is emitted by the DB
// trigger when rows actually change.
func (s *Store) ResolveNotifications(ctx context.Context, filter domain.NotificationResolveFilter, resolvedAt time.Time) (int, error) {
	if filter.ProjectID == "" {
		return 0, errors.New("resolve notifications: missing project id")
	}
	if resolvedAt.IsZero() {
		resolvedAt = time.Now().UTC()
	}
	statuses := filter.Statuses
	if len(statuses) == 0 {
		statuses = []domain.NotificationStatus{domain.NotificationUnread, domain.NotificationRead}
	}

	clauses := []string{"project_id = ?"}
	args := []any{filter.ProjectID}
	if filter.SessionID != nil {
		clauses = append(clauses, "session_id = ?")
		args = append(args, *filter.SessionID)
	}
	if len(statuses) > 0 {
		ph := placeholders(len(statuses))
		clauses = append(clauses, "status IN ("+ph+")")
		for _, st := range statuses {
			args = append(args, st)
		}
	}
	if len(filter.Types) > 0 {
		ph := placeholders(len(filter.Types))
		clauses = append(clauses, "type IN ("+ph+")")
		for _, typ := range filter.Types {
			args = append(args, typ)
		}
	}
	if filter.PRURL != "" {
		clauses = append(clauses, "(json_extract(subject_json, '$.prUrl') = ? OR json_extract(data_json, '$.pr.url') = ? OR json_extract(data_json, '$.intent.context.prUrl') = ?)")
		args = append(args, filter.PRURL, filter.PRURL, filter.PRURL)
	}
	if len(filter.DedupeKeyPrefixes) > 0 {
		ors := make([]string, 0, len(filter.DedupeKeyPrefixes))
		for _, prefix := range filter.DedupeKeyPrefixes {
			ors = append(ors, "dedupe_key LIKE ?")
			args = append(args, prefix+"%")
		}
		clauses = append(clauses, "("+strings.Join(ors, " OR ")+")")
	}

	// #nosec G202 -- clauses are selected from fixed strings above and all values are bound placeholders.
	query := "UPDATE notifications SET status = 'resolved', resolved_at = ?, updated_at = ? WHERE " + strings.Join(clauses, " AND ")
	args = append([]any{resolvedAt, resolvedAt}, args...)

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	res, err := s.writeDB.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("resolve notifications: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("resolve notifications rows affected: %w", err)
	}
	return int(n), nil
}

// GetNotification returns one notification by id, or ok=false when absent.
func (s *Store) GetNotification(ctx context.Context, id domain.NotificationID) (domain.Notification, bool, error) {
	row, err := s.qr.GetNotification(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Notification{}, false, nil
	}
	if err != nil {
		return domain.Notification{}, false, fmt.Errorf("get notification %s: %w", id, err)
	}
	n, err := notificationFromGen(row)
	if err != nil {
		return domain.Notification{}, false, fmt.Errorf("get notification %s: %w", id, err)
	}
	return n, true, nil
}

// ListNotificationsByProject returns newest notifications for a project.
func (s *Store) ListNotificationsByProject(ctx context.Context, projectID domain.ProjectID, limit int) ([]domain.Notification, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.qr.ListNotificationsByProject(ctx, gen.ListNotificationsByProjectParams{ProjectID: projectID, Limit: int64(limit)})
	if err != nil {
		return nil, fmt.Errorf("list notifications for project %s: %w", projectID, err)
	}
	return notificationsFromGen(rows)
}

// ListNotificationsBySession returns newest notifications for a session.
func (s *Store) ListNotificationsBySession(ctx context.Context, sessionID domain.SessionID, limit int) ([]domain.Notification, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.qr.ListNotificationsBySession(ctx, gen.ListNotificationsBySessionParams{SessionID: &sessionID, Limit: int64(limit)})
	if err != nil {
		return nil, fmt.Errorf("list notifications for session %s: %w", sessionID, err)
	}
	return notificationsFromGen(rows)
}

func notificationInsertParams(n domain.Notification) (gen.InsertNotificationParams, error) {
	subject, data, actions, err := notificationJSON(n)
	if err != nil {
		return gen.InsertNotificationParams{}, err
	}
	return gen.InsertNotificationParams{
		ID: n.ID, ProjectID: n.ProjectID, SessionID: n.SessionID,
		Type: n.Type, Priority: n.Priority, Status: n.Status, Source: n.Source,
		DedupeKey: n.DedupeKey, Fingerprint: n.Fingerprint,
		Title: n.Title, Summary: n.Summary, Body: n.Body,
		SubjectJson: subject, DataJson: data, ActionsJson: actions,
		ReadAt: nullPtrTime(n.ReadAt), DismissedAt: nullPtrTime(n.DismissedAt), ResolvedAt: nullPtrTime(n.ResolvedAt),
		OccurredAt: n.OccurredAt, CreatedAt: n.CreatedAt, UpdatedAt: n.UpdatedAt,
	}, nil
}

func notificationUpdateParams(n domain.Notification) (gen.UpdateNotificationContentParams, error) {
	subject, data, actions, err := notificationJSON(n)
	if err != nil {
		return gen.UpdateNotificationContentParams{}, err
	}
	return gen.UpdateNotificationContentParams{
		ID:   n.ID,
		Type: n.Type, Priority: n.Priority, Status: n.Status, Source: n.Source,
		Fingerprint: n.Fingerprint,
		Title:       n.Title, Summary: n.Summary, Body: n.Body,
		SubjectJson: subject, DataJson: data, ActionsJson: actions,
		ReadAt: nullPtrTime(n.ReadAt), DismissedAt: nullPtrTime(n.DismissedAt), ResolvedAt: nullPtrTime(n.ResolvedAt),
		OccurredAt: n.OccurredAt, UpdatedAt: n.UpdatedAt,
	}, nil
}

func notificationJSON(n domain.Notification) (subject, data, actions string, err error) {
	n = n.Normalize()
	subjectBytes, err := json.Marshal(n.Subject)
	if err != nil {
		return "", "", "", fmt.Errorf("marshal notification subject: %w", err)
	}
	dataBytes, err := json.Marshal(n.Data)
	if err != nil {
		return "", "", "", fmt.Errorf("marshal notification data: %w", err)
	}
	actionBytes, err := json.Marshal(n.Actions)
	if err != nil {
		return "", "", "", fmt.Errorf("marshal notification actions: %w", err)
	}
	return string(subjectBytes), string(dataBytes), string(actionBytes), nil
}

func notificationsFromGen(rows []gen.Notification) ([]domain.Notification, error) {
	out := make([]domain.Notification, 0, len(rows))
	for _, row := range rows {
		n, err := notificationFromGen(row)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, nil
}

func notificationFromGen(row gen.Notification) (domain.Notification, error) {
	var subject domain.NotificationSubject
	if err := json.Unmarshal([]byte(row.SubjectJson), &subject); err != nil {
		return domain.Notification{}, fmt.Errorf("unmarshal notification subject: %w", err)
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(row.DataJson), &data); err != nil {
		return domain.Notification{}, fmt.Errorf("unmarshal notification data: %w", err)
	}
	var actions []domain.NotificationAction
	if err := json.Unmarshal([]byte(row.ActionsJson), &actions); err != nil {
		return domain.Notification{}, fmt.Errorf("unmarshal notification actions: %w", err)
	}
	return domain.Notification{
		ID: row.ID, Type: row.Type, Priority: row.Priority, Status: row.Status,
		ProjectID: row.ProjectID, SessionID: row.SessionID,
		Source: row.Source, DedupeKey: row.DedupeKey, Fingerprint: row.Fingerprint,
		Title: row.Title, Summary: row.Summary, Body: row.Body,
		Subject: subject, Data: data, Actions: actions,
		OccurredAt: row.OccurredAt, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		ReadAt: ptrTime(row.ReadAt), DismissedAt: ptrTime(row.DismissedAt), ResolvedAt: ptrTime(row.ResolvedAt),
	}.Normalize(), nil
}

func nullPtrTime(t *time.Time) sql.NullTime {
	if t == nil || t.IsZero() {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *t, Valid: true}
}

func ptrTime(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	parts := make([]string, n)
	for i := range parts {
		parts[i] = "?"
	}
	return strings.Join(parts, ",")
}
