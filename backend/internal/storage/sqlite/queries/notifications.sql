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


-- name: GetUnreadNotificationByDedupe :one
SELECT *
FROM notifications
WHERE session_id = ? AND type = ? AND pr_url = ? AND status = 'unread'
LIMIT 1;
