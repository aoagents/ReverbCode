-- name: NextSessionNum :one
SELECT COALESCE(MAX(num), 0) + 1 AS next FROM sessions WHERE project_id = ?;

-- name: InsertSession :exec
INSERT INTO sessions (
    id, project_id, num, issue_id, kind, harness,
    session_state, termination_reason, is_alive,
    activity_state, activity_last_at, activity_source,
    detecting_attempts, detecting_started_at, detecting_evidence_hash,
    branch, workspace_path, runtime_handle_id, runtime_name, agent_session_id, prompt,
    created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: UpdateSession :exec
UPDATE sessions SET
    issue_id = ?, kind = ?, harness = ?,
    session_state = ?, termination_reason = ?, is_alive = ?,
    activity_state = ?, activity_last_at = ?, activity_source = ?,
    detecting_attempts = ?, detecting_started_at = ?, detecting_evidence_hash = ?,
    branch = ?, workspace_path = ?, runtime_handle_id = ?, runtime_name = ?, agent_session_id = ?, prompt = ?,
    updated_at = ?
WHERE id = ?;

-- name: GetSession :one
SELECT * FROM sessions WHERE id = ?;

-- name: ListSessionsByProject :many
SELECT * FROM sessions WHERE project_id = ? ORDER BY num;

-- name: ListAllSessions :many
SELECT * FROM sessions ORDER BY project_id, num;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE id = ?;
