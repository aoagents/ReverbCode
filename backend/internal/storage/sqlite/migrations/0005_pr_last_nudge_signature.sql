-- Summary: persist per-PR reaction dedup signatures so agent nudges
-- (CI failure, review feedback, merge conflict) survive a daemon restart
-- instead of re-firing on the first post-restart observer poll.
--
-- The column carries a small JSON document encoded by lifecycle.Manager:
--   {"seen":{<reaction_key>:<signature>}, "attempts":{<reaction_key>:<count>}}
-- where reaction_key uniquely identifies a nudge target (e.g. "ci:<url>:<check>",
-- "review:<url>", "merge-conflict:<url>") and signature is the content
-- fingerprint that gates whether a re-fire is warranted.
-- +goose Up
-- +goose StatementBegin
ALTER TABLE pr ADD COLUMN last_nudge_signature TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE pr DROP COLUMN last_nudge_signature;
-- +goose StatementEnd
