-- name: CreateNotification :one
INSERT INTO notifications (
    id, session_id, project_id, pr_url, type, title, body, status, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: ListUnreadNotifications :many
SELECT *
FROM notifications
WHERE status = 'unread'
ORDER BY created_at DESC
LIMIT ?;

-- name: ListUnreadNotificationsByProject :many
SELECT *
FROM notifications
WHERE project_id = ? AND status = 'unread'
ORDER BY created_at DESC
LIMIT ?;
