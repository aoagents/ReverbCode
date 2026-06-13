-- Configurable AO code review (issue #192). review holds one row per worker
-- session under review (session_id UNIQUE); a repeat trigger reuses the row.
-- review_run holds the per-pass facts. The reviewer agent posts its review to
-- the PR itself; `ao review submit` records the verdict and body on the run.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE review (
    id                 TEXT PRIMARY KEY,
    session_id         TEXT NOT NULL UNIQUE REFERENCES sessions (id) ON DELETE CASCADE,
    project_id         TEXT NOT NULL REFERENCES projects (id),
    harness            TEXT NOT NULL,
    pr_url             TEXT NOT NULL DEFAULT '',
    -- runtime handle id of the live reviewer pane, reused across passes and
    -- exposed so the UI can attach its terminal over /mux.
    reviewer_handle_id TEXT NOT NULL DEFAULT '',
    created_at         TIMESTAMP NOT NULL,
    updated_at         TIMESTAMP NOT NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE review_run (
    id          TEXT PRIMARY KEY,
    review_id   TEXT NOT NULL REFERENCES review (id) ON DELETE CASCADE,
    session_id  TEXT NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    harness     TEXT NOT NULL,
    pr_url      TEXT NOT NULL DEFAULT '',
    -- the commit the pass reviewed; lets a repeat trigger for the same head
    -- short-circuit to the existing run.
    target_sha  TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT 'running',
    verdict     TEXT NOT NULL DEFAULT '',
    body        TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMP NOT NULL
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE review_run;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE review;
-- +goose StatementEnd
