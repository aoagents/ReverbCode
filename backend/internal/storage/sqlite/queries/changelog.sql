-- name: ReadChangeLogAfter :many
SELECT seq, project_id, session_id, event_type, payload, created_at
FROM change_log WHERE seq > ? ORDER BY seq LIMIT ?;


-- name: MaxChangeLogSeq :one
SELECT CAST(COALESCE(MAX(seq), 0) AS INTEGER) AS seq FROM change_log;

-- name: DeleteChangeLogForSession :execrows
-- DeleteChangeLogForSession removes all change_log rows referencing the given
-- session id. Used by Store.DeleteSession to satisfy the change_log → sessions
-- foreign key when removing a seed-state row whose orphan removal would
-- otherwise be blocked by the existing session_created CDC event.
DELETE FROM change_log WHERE session_id = ?;
