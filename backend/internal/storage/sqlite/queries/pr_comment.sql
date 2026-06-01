-- name: InsertPRComment :exec
INSERT INTO pr_comment (pr_url, comment_id, author, file, line, body, resolved, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: DeletePRComments :exec
DELETE FROM pr_comment WHERE pr_url = ?;

-- name: ListPRComments :many
SELECT pr_url, comment_id, author, file, line, body, resolved, created_at
FROM pr_comment WHERE pr_url = ? ORDER BY created_at, comment_id;
