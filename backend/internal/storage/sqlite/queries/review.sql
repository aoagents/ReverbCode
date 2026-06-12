-- name: UpsertReview :exec
INSERT INTO review (id, session_id, project_id, harness, pr_url, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (session_id) DO UPDATE SET
    harness = excluded.harness,
    pr_url = excluded.pr_url,
    updated_at = excluded.updated_at;

-- name: GetReviewBySession :one
SELECT id, session_id, project_id, harness, pr_url, created_at, updated_at
FROM review WHERE session_id = ?;

-- name: InsertReviewRun :exec
INSERT INTO review_run (id, review_id, session_id, harness, pr_url, status, verdict, iteration, body, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: UpdateReviewRunResult :exec
UPDATE review_run SET status = ?, verdict = ?, body = ? WHERE id = ?;

-- name: GetLatestReviewRunBySession :one
SELECT id, review_id, session_id, harness, pr_url, status, verdict, iteration, body, created_at
FROM review_run WHERE session_id = ? ORDER BY iteration DESC, created_at DESC LIMIT 1;

-- name: ListReviewRunsBySession :many
SELECT id, review_id, session_id, harness, pr_url, status, verdict, iteration, body, created_at
FROM review_run WHERE session_id = ? ORDER BY iteration DESC, created_at DESC;
