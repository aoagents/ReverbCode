-- name: NextSessionNum :one
SELECT COALESCE(MAX(num), 0) + 1 AS next FROM sessions WHERE project_id = ?;

-- name: InsertSession :exec
INSERT INTO sessions (
    id, project_id, num, issue_id, kind, harness, display_name,
    activity_state, activity_last_at, first_signal_at, is_terminated,
    branch, workspace_path, runtime_handle_id, agent_session_id, prompt,
    created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: UpdateSession :exec
UPDATE sessions SET
    issue_id = ?, kind = ?, harness = ?, display_name = ?,
    activity_state = ?, activity_last_at = ?, first_signal_at = ?, is_terminated = ?,
    branch = ?, workspace_path = ?, runtime_handle_id = ?, agent_session_id = ?, prompt = ?,
    updated_at = ?
WHERE id = ?;

-- name: GetSession :one
SELECT id, project_id, num, issue_id, kind, harness,
    activity_state, activity_last_at, is_terminated, branch, workspace_path,
    runtime_handle_id, agent_session_id, prompt, created_at, updated_at, display_name, first_signal_at, archived_at
FROM sessions WHERE id = ?;

-- name: ListSessionsByProject :many
SELECT id, project_id, num, issue_id, kind, harness,
    activity_state, activity_last_at, is_terminated, branch, workspace_path,
    runtime_handle_id, agent_session_id, prompt, created_at, updated_at, display_name, first_signal_at, archived_at
FROM sessions WHERE project_id = ? ORDER BY num;

-- name: ListAllSessions :many
SELECT id, project_id, num, issue_id, kind, harness,
    activity_state, activity_last_at, is_terminated, branch, workspace_path,
    runtime_handle_id, agent_session_id, prompt, created_at, updated_at, display_name, first_signal_at, archived_at
FROM sessions ORDER BY project_id, num;


-- name: RenameSession :execrows
UPDATE sessions SET display_name = ?, updated_at = ? WHERE id = ?;

-- name: ArchiveSession :execrows
-- ArchiveSession soft-hides a terminated session. The is_terminated guard is
-- in the statement so a concurrent restore can't race the service's
-- pre-check; the archived_at IS NULL guard makes a repeat archive a no-op
-- (0 rows) instead of moving the timestamp.
UPDATE sessions SET archived_at = ?, updated_at = ?
WHERE id = ? AND is_terminated = 1 AND archived_at IS NULL;

-- name: UnarchiveSession :execrows
UPDATE sessions SET archived_at = NULL, updated_at = ?
WHERE id = ? AND archived_at IS NOT NULL;

-- name: SessionIsSeed :one
-- SessionIsSeed reports whether the session id matches a row still in seed
-- state (see DeleteSeedSession for the conditions). Callers probe with this
-- before touching change_log so that DeleteSession is a true no-op for live
-- sessions instead of silently destroying their CDC events. Returns 0 when
-- the row does not exist OR has progressed past seed state.
SELECT EXISTS(
    SELECT 1 FROM sessions
    WHERE id = ?
      AND is_terminated = 0
      AND workspace_path = ''
      AND runtime_handle_id = ''
      AND agent_session_id = ''
      AND prompt = ''
) AS is_seed;

-- NOTE: the `DELETE FROM sessions WHERE id = ? AND <seed-state predicates>`
-- statement is intentionally NOT a sqlc query — same sqlc 1.31 SQLite-parser
-- bug as documented in queries/changelog.sql: trailing string literals (and
-- placeholders) on the RHS of `=` in a DELETE get silently stripped, so the
-- generated SQL ends up mid-clause and the row count is meaningless. The
-- store runs that DELETE directly via tx.ExecContext inside
-- Store.DeleteSession, inside the same transaction as the SessionIsSeed
-- probe and the raw change_log cleanup.
