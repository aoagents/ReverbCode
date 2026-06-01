-- name: UpsertPR :exec
INSERT INTO pr (url, session_id, number, pr_state, review_decision, ci_state, mergeability, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (url) DO UPDATE SET
    session_id = excluded.session_id,
    number = excluded.number,
    pr_state = excluded.pr_state,
    review_decision = excluded.review_decision,
    ci_state = excluded.ci_state,
    mergeability = excluded.mergeability,
    updated_at = excluded.updated_at;

-- name: GetPR :one
SELECT * FROM pr WHERE url = ?;

-- name: ListPRsBySession :many
SELECT * FROM pr WHERE session_id = ? ORDER BY updated_at DESC;

