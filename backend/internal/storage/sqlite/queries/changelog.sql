-- name: ReadChangeLogAfter :many
SELECT seq, project_id, session_id, event_type, payload, created_at
FROM change_log WHERE seq > ? ORDER BY seq LIMIT ?;

-- name: ReadChangeLogAfterForProject :many
SELECT seq, project_id, session_id, event_type, payload, created_at
FROM change_log WHERE project_id = ? AND seq > ? ORDER BY seq LIMIT ?;

-- name: MaxChangeLogSeq :one
SELECT COALESCE(MAX(seq), 0) AS seq FROM change_log;
