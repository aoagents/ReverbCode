-- name: ReadChangeLogAfter :many
SELECT seq, project_id, session_id, event_type, payload, created_at
FROM change_log WHERE seq > ? ORDER BY seq LIMIT ?;


-- name: MaxChangeLogSeq :one
SELECT CAST(COALESCE(MAX(seq), 0) AS INTEGER) AS seq FROM change_log;
