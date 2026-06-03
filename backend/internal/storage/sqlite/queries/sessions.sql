-- name: NextSessionNum :one
SELECT COALESCE(MAX(num), 0) + 1 AS next FROM sessions WHERE project_id = ?;

-- name: InsertSession :exec
INSERT INTO sessions (
    id, project_id, num, issue_id, kind, harness, display_name,
    activity_state, activity_last_at, is_terminated,
    branch, workspace_path, runtime_handle_id, agent_session_id, prompt,
    created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: UpdateSession :exec
UPDATE sessions SET
    issue_id = ?, kind = ?, harness = ?, display_name = ?,
    activity_state = ?, activity_last_at = ?, is_terminated = ?,
    branch = ?, workspace_path = ?, runtime_handle_id = ?, agent_session_id = ?, prompt = ?,
    updated_at = ?
WHERE id = ?;

-- name: GetSession :one
SELECT id, project_id, num, issue_id, kind, harness,
    activity_state, activity_last_at, is_terminated, branch, workspace_path,
    runtime_handle_id, agent_session_id, prompt, created_at, updated_at, display_name
FROM sessions WHERE id = ?;

-- name: ListSessionsByProject :many
SELECT id, project_id, num, issue_id, kind, harness,
    activity_state, activity_last_at, is_terminated, branch, workspace_path,
    runtime_handle_id, agent_session_id, prompt, created_at, updated_at, display_name
FROM sessions WHERE project_id = ? ORDER BY num;

-- name: ListAllSessions :many
SELECT id, project_id, num, issue_id, kind, harness,
    activity_state, activity_last_at, is_terminated, branch, workspace_path,
    runtime_handle_id, agent_session_id, prompt, created_at, updated_at, display_name
FROM sessions ORDER BY project_id, num;


-- name: RenameSession :execrows
UPDATE sessions SET display_name = ?, updated_at = ? WHERE id = ?;
