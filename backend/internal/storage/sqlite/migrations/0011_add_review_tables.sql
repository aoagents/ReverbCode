-- Configurable AO code review (issue #192). review holds one row per worker
-- session under review (session_id UNIQUE); a repeat trigger reuses the row.
-- review_run holds the per-pass facts. The review body is not persisted — it is
-- posted to the SCM provider and flows to the worker through the SCM observer.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE review (
    id          TEXT PRIMARY KEY,
    session_id  TEXT NOT NULL UNIQUE REFERENCES sessions (id) ON DELETE CASCADE,
    project_id  TEXT NOT NULL REFERENCES projects (id),
    harness     TEXT NOT NULL,
    pr_url      TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMP NOT NULL,
    updated_at  TIMESTAMP NOT NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE review_run (
    id          TEXT PRIMARY KEY,
    review_id   TEXT NOT NULL REFERENCES review (id) ON DELETE CASCADE,
    session_id  TEXT NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    harness     TEXT NOT NULL,
    pr_url      TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'complete', 'failed')),
    verdict     TEXT NOT NULL DEFAULT ''
        CHECK (verdict IN ('', 'approved', 'changes_requested')),
    iteration   INTEGER NOT NULL DEFAULT 1,
    created_at  TIMESTAMP NOT NULL,
    updated_at  TIMESTAMP NOT NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_review_run_session ON review_run (session_id, iteration);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_review_run_session;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE review_run;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE review;
-- +goose StatementEnd
