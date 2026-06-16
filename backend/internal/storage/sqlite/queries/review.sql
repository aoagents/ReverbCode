-- name: UpsertReview :exec
INSERT INTO review (id, session_id, project_id, harness, pr_url, reviewer_handle_id, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (session_id, pr_url) DO UPDATE SET
    harness = excluded.harness,
    reviewer_handle_id = excluded.reviewer_handle_id,
    updated_at = excluded.updated_at;

-- name: GetReviewBySessionAndPR :one
SELECT id, session_id, project_id, harness, pr_url, reviewer_handle_id, created_at, updated_at
FROM review WHERE session_id = ? AND pr_url = ?;

-- name: ListReviewsBySession :many
SELECT id, session_id, project_id, harness, pr_url, reviewer_handle_id, created_at, updated_at
FROM review WHERE session_id = ? ORDER BY updated_at DESC;

-- name: InsertReviewRun :exec
INSERT INTO review_run (id, review_id, session_id, harness, pr_url, target_sha, status, verdict, body, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: UpdateReviewRunResult :execrows
UPDATE review_run SET status = ?, verdict = ?, body = ? WHERE id = ? AND status = 'running';

-- name: GetReviewRun :one
SELECT id, review_id, session_id, harness, pr_url, target_sha, status, verdict, body, created_at
FROM review_run WHERE id = ?;

-- name: GetReviewRunBySessionPRAndSHA :one
SELECT id, review_id, session_id, harness, pr_url, target_sha, status, verdict, body, created_at
FROM review_run WHERE session_id = ? AND pr_url = ? AND target_sha = ? ORDER BY created_at DESC LIMIT 1;

-- name: ListReviewRunsBySession :many
SELECT id, review_id, session_id, harness, pr_url, target_sha, status, verdict, body, created_at
FROM review_run WHERE session_id = ? ORDER BY created_at DESC;

-- name: ListReviewRunsBySessionAndPR :many
SELECT id, review_id, session_id, harness, pr_url, target_sha, status, verdict, body, created_at
FROM review_run WHERE session_id = ? AND pr_url = ? ORDER BY created_at DESC;
