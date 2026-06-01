-- name: UpsertPRCheck :exec
INSERT INTO pr_checks (pr_url, name, commit_hash, status, url, log_tail, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (pr_url, name, commit_hash) DO UPDATE SET
    status = excluded.status,
    url = excluded.url,
    log_tail = excluded.log_tail;

-- name: ListChecksByPR :many
SELECT pr_url, name, commit_hash, status, url, log_tail, created_at
FROM pr_checks WHERE pr_url = ? ORDER BY name, created_at;
