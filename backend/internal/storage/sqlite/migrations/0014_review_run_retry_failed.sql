-- Failed review runs are durable diagnostics, not idempotency winners. Exclude
-- them from the session/SHA unique index so a user can install a missing
-- reviewer harness and retry the same commit while keeping the failed attempt
-- visible in history.

-- +goose Up
-- +goose StatementBegin
DROP INDEX idx_review_run_session_sha;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX idx_review_run_session_sha
    ON review_run (session_id, target_sha)
    WHERE target_sha != '' AND status != 'failed';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX idx_review_run_session_sha;
-- +goose StatementEnd

-- +goose StatementBegin
DELETE FROM review_run
WHERE target_sha != ''
  AND rowid NOT IN (
    SELECT rowid FROM (
      SELECT rowid,
             ROW_NUMBER() OVER (
               PARTITION BY session_id, target_sha
               ORDER BY CASE status WHEN 'complete' THEN 0 WHEN 'running' THEN 1 ELSE 2 END,
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
