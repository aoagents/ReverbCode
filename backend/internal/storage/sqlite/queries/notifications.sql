-- name: InsertNotification :exec
INSERT INTO notifications (
    id, project_id, session_id, type, priority, status, source, dedupe_key, fingerprint,
    title, summary, body, subject_json, data_json, actions_json,
    read_at, dismissed_at, resolved_at, occurred_at, created_at, updated_at
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: UpdateNotificationContent :exec
UPDATE notifications
SET
    type = ?,
    priority = ?,
    status = ?,
    source = ?,
    fingerprint = ?,
    title = ?,
    summary = ?,
    body = ?,
    subject_json = ?,
    data_json = ?,
    actions_json = ?,
    read_at = ?,
    dismissed_at = ?,
    resolved_at = ?,
    occurred_at = ?,
    updated_at = ?
WHERE id = ?;

-- name: ResolveNotification :execrows
UPDATE notifications
SET status = 'resolved', resolved_at = ?, updated_at = ?
WHERE id = ? AND status IN ('unread', 'read');

-- name: GetNotification :one
SELECT
    id, project_id, session_id, type, priority, status, source, dedupe_key, fingerprint,
    title, summary, body, subject_json, data_json, actions_json,
    read_at, dismissed_at, resolved_at, occurred_at, created_at, updated_at
FROM notifications
WHERE id = ?;

-- name: GetNotificationByDedupeKey :one
SELECT
    id, project_id, session_id, type, priority, status, source, dedupe_key, fingerprint,
    title, summary, body, subject_json, data_json, actions_json,
    read_at, dismissed_at, resolved_at, occurred_at, created_at, updated_at
FROM notifications
WHERE project_id = ? AND dedupe_key = ?;

-- name: ListNotificationsByProject :many
SELECT
    id, project_id, session_id, type, priority, status, source, dedupe_key, fingerprint,
    title, summary, body, subject_json, data_json, actions_json,
    read_at, dismissed_at, resolved_at, occurred_at, created_at, updated_at
FROM notifications
WHERE project_id = ?
ORDER BY created_at DESC
LIMIT ?;

-- name: ListNotificationsBySession :many
SELECT
    id, project_id, session_id, type, priority, status, source, dedupe_key, fingerprint,
    title, summary, body, subject_json, data_json, actions_json,
    read_at, dismissed_at, resolved_at, occurred_at, created_at, updated_at
FROM notifications
WHERE session_id = ?
ORDER BY created_at DESC
LIMIT ?;
