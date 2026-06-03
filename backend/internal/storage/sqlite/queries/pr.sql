-- name: UpsertPR :exec
INSERT INTO pr (url, session_id, number, pr_state, review_decision, ci_state, mergeability, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (url) DO UPDATE SET
    number = excluded.number,
    pr_state = excluded.pr_state,
    review_decision = excluded.review_decision,
    ci_state = excluded.ci_state,
    mergeability = excluded.mergeability,
    updated_at = excluded.updated_at;

-- name: GetPR :one
SELECT url, session_id, number, pr_state, review_decision, ci_state, mergeability, updated_at
FROM pr
WHERE url = ?;

-- name: ListPRsBySession :many
SELECT url, session_id, number, pr_state, review_decision, ci_state, mergeability, updated_at
FROM pr
WHERE session_id = ?
ORDER BY updated_at DESC;


-- name: GetDisplayPRFactsBySession :one
SELECT
    pr.url,
    pr.number,
    pr.pr_state,
    pr.review_decision,
    pr.ci_state,
    pr.mergeability,
    EXISTS (
        SELECT 1
        FROM pr_comment
        WHERE pr_comment.pr_url = pr.url
          AND pr_comment.resolved = 0
    ) AS review_comments
FROM pr
WHERE pr.session_id = ?
ORDER BY
    CASE WHEN pr.pr_state NOT IN ('merged', 'closed') THEN 0 ELSE 1 END,
    pr.updated_at DESC
LIMIT 1;
