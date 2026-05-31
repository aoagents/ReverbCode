-- name: UpsertPRCheck :exec
INSERT INTO pr_checks (pr_url, name, commit_hash, status, url, log_tail, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (pr_url, name, commit_hash) DO UPDATE SET
    status = excluded.status,
    url = excluded.url,
    log_tail = excluded.log_tail;

-- name: ListRecentChecks :many
SELECT status, commit_hash, created_at FROM pr_checks
WHERE pr_url = ? AND name = ?
ORDER BY created_at DESC LIMIT ?;

-- name: ListChecksByPR :many
SELECT * FROM pr_checks WHERE pr_url = ? ORDER BY name, created_at;
