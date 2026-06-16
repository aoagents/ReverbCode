-- A partial unique index backstops the per-worker lock in internal/review: it
-- prevents two concurrent (or cross-restart) Trigger calls from recording two
-- review_run rows for the same worker session at the same reviewed commit
-- (issue #242). Rows with an empty target_sha (head not yet observed) are
-- excluded so they aren't blocked — the engine lock still serialises those.

-- +goose Up
-- Pre-#242 daemons could already have recorded duplicate (session_id,
-- target_sha) rows from the un-serialised double-spawn. CREATE UNIQUE INDEX
-- would fail on that data and wedge daemon startup, so collapse each duplicate
-- group to a single survivor first. We keep a completed pass over a still-running
-- one (it carries the reviewer's verdict/body), then the newest by created_at —
-- the same row a post-migration GetReviewRunBySessionAndSHA lookup would return.
-- +goose StatementBegin
DELETE FROM review_run
WHERE target_sha != ''
  AND rowid NOT IN (
    SELECT rowid FROM (
      SELECT rowid,
             ROW_NUMBER() OVER (
               PARTITION BY session_id, target_sha
               ORDER BY CASE status WHEN 'complete' THEN 0 ELSE 1 END,
                        created_at DESC,
                        rowid DESC
             ) AS rn
      FROM review_run
      WHERE target_sha != ''
    )
    WHERE rn = 1
  );
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX idx_review_run_session_sha
    ON review_run (session_id, target_sha) WHERE target_sha != '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX idx_review_run_session_sha;
-- +goose StatementEnd
