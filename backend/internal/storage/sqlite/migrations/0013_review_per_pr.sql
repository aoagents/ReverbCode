-- Review rows are now scoped to one pull request within a worker session.
-- Workspace sessions can own multiple PRs, so session_id alone is not a
-- stable review identity.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE review_new (
    id                 TEXT PRIMARY KEY,
    session_id         TEXT NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    project_id         TEXT NOT NULL REFERENCES projects (id),
    harness            TEXT NOT NULL,
    pr_url             TEXT NOT NULL DEFAULT '',
    reviewer_handle_id TEXT NOT NULL DEFAULT '',
    created_at         TIMESTAMP NOT NULL,
    updated_at         TIMESTAMP NOT NULL,
    UNIQUE (session_id, pr_url)
);

INSERT INTO review_new (id, session_id, project_id, harness, pr_url, reviewer_handle_id, created_at, updated_at)
SELECT id, session_id, project_id, harness, pr_url, reviewer_handle_id, created_at, updated_at
FROM review;

DROP TABLE review;
ALTER TABLE review_new RENAME TO review;

CREATE INDEX idx_review_session ON review (session_id);
CREATE INDEX idx_review_run_session_pr_sha ON review_run (session_id, pr_url, target_sha, created_at);
CREATE INDEX idx_review_run_session_pr_created ON review_run (session_id, pr_url, created_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_review_run_session_pr_created;
DROP INDEX IF EXISTS idx_review_run_session_pr_sha;
DROP INDEX IF EXISTS idx_review_session;

CREATE TABLE review_old (
    id                 TEXT PRIMARY KEY,
    session_id         TEXT NOT NULL UNIQUE REFERENCES sessions (id) ON DELETE CASCADE,
    project_id         TEXT NOT NULL REFERENCES projects (id),
    harness            TEXT NOT NULL,
    pr_url             TEXT NOT NULL DEFAULT '',
    reviewer_handle_id TEXT NOT NULL DEFAULT '',
    created_at         TIMESTAMP NOT NULL,
    updated_at         TIMESTAMP NOT NULL
);

INSERT OR IGNORE INTO review_old (id, session_id, project_id, harness, pr_url, reviewer_handle_id, created_at, updated_at)
SELECT id, session_id, project_id, harness, pr_url, reviewer_handle_id, created_at, updated_at
FROM review
ORDER BY updated_at DESC;

DROP TABLE review;
ALTER TABLE review_old RENAME TO review;
-- +goose StatementEnd
