-- Summary: extend PR persistence for provider-neutral SCM observations, CI/check detail,
-- review-thread storage, and semantic hashes used by the SCM observer.
-- +goose Up
-- +goose StatementBegin
ALTER TABLE pr ADD COLUMN provider TEXT NOT NULL DEFAULT '';
ALTER TABLE pr ADD COLUMN host TEXT NOT NULL DEFAULT '';
ALTER TABLE pr ADD COLUMN repo TEXT NOT NULL DEFAULT '';
ALTER TABLE pr ADD COLUMN source_branch TEXT NOT NULL DEFAULT '';
ALTER TABLE pr ADD COLUMN target_branch TEXT NOT NULL DEFAULT '';
ALTER TABLE pr ADD COLUMN head_sha TEXT NOT NULL DEFAULT '';
ALTER TABLE pr ADD COLUMN title TEXT NOT NULL DEFAULT '';
ALTER TABLE pr ADD COLUMN additions INTEGER NOT NULL DEFAULT 0;
ALTER TABLE pr ADD COLUMN deletions INTEGER NOT NULL DEFAULT 0;
ALTER TABLE pr ADD COLUMN changed_files INTEGER NOT NULL DEFAULT 0;
ALTER TABLE pr ADD COLUMN author TEXT NOT NULL DEFAULT '';
ALTER TABLE pr ADD COLUMN base_sha TEXT NOT NULL DEFAULT '';
ALTER TABLE pr ADD COLUMN merge_commit_sha TEXT NOT NULL DEFAULT '';
ALTER TABLE pr ADD COLUMN is_draft INTEGER NOT NULL DEFAULT 0;
ALTER TABLE pr ADD COLUMN is_merged INTEGER NOT NULL DEFAULT 0;
ALTER TABLE pr ADD COLUMN is_closed INTEGER NOT NULL DEFAULT 0;
ALTER TABLE pr ADD COLUMN provider_state TEXT NOT NULL DEFAULT '';
ALTER TABLE pr ADD COLUMN provider_mergeable TEXT NOT NULL DEFAULT '';
ALTER TABLE pr ADD COLUMN provider_merge_state_status TEXT NOT NULL DEFAULT '';
ALTER TABLE pr ADD COLUMN html_url TEXT NOT NULL DEFAULT '';
ALTER TABLE pr ADD COLUMN created_at_provider TIMESTAMP;
ALTER TABLE pr ADD COLUMN updated_at_provider TIMESTAMP;
ALTER TABLE pr ADD COLUMN merged_at_provider TIMESTAMP;
ALTER TABLE pr ADD COLUMN closed_at_provider TIMESTAMP;
ALTER TABLE pr ADD COLUMN metadata_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE pr ADD COLUMN ci_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE pr ADD COLUMN review_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE pr ADD COLUMN observed_at TIMESTAMP;
ALTER TABLE pr ADD COLUMN ci_observed_at TIMESTAMP;
ALTER TABLE pr ADD COLUMN review_observed_at TIMESTAMP;

ALTER TABLE pr_checks ADD COLUMN conclusion TEXT NOT NULL DEFAULT '';
ALTER TABLE pr_checks ADD COLUMN details TEXT NOT NULL DEFAULT '';

ALTER TABLE pr_comment ADD COLUMN thread_id TEXT NOT NULL DEFAULT '';
ALTER TABLE pr_comment ADD COLUMN url TEXT NOT NULL DEFAULT '';
ALTER TABLE pr_comment ADD COLUMN is_bot INTEGER NOT NULL DEFAULT 0;

CREATE TABLE pr_review_threads (
    pr_url        TEXT NOT NULL REFERENCES pr (url) ON DELETE CASCADE,
    thread_id     TEXT NOT NULL,
    path          TEXT NOT NULL DEFAULT '',
    line          INTEGER NOT NULL DEFAULT 0,
    resolved      INTEGER NOT NULL DEFAULT 0,
    is_bot        INTEGER NOT NULL DEFAULT 0,
    semantic_hash TEXT NOT NULL DEFAULT '',
    updated_at    TIMESTAMP NOT NULL,
    PRIMARY KEY (pr_url, thread_id)
);
CREATE INDEX idx_pr_review_threads_lookup ON pr_review_threads (pr_url, updated_at);
-- +goose StatementEnd

-- +goose StatementBegin
-- Widen change_log.event_type CHECK to include the new pr_review_thread_* events.
-- SQLite cannot ALTER an in-place CHECK constraint; this drop/recreate is safe
-- because change_log is append-only and the SSE broadcaster re-seeks to head on
-- restart (no consumer durably stores a change_log offset across this migration).
DROP INDEX IF EXISTS idx_change_log_project;
DROP TABLE change_log;
CREATE TABLE change_log (
    seq        INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id TEXT NOT NULL REFERENCES projects (id),
    session_id TEXT REFERENCES sessions (id),
    event_type TEXT NOT NULL
        CHECK (event_type IN (
            'session_created',
            'session_updated',
            'pr_created',
            'pr_updated',
            'pr_check_recorded',
            'pr_review_thread_added',
            'pr_review_thread_resolved'
        )),
    payload    TEXT NOT NULL CHECK (json_valid(payload)),
    created_at TIMESTAMP NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_change_log_project ON change_log (project_id, seq);
-- +goose StatementEnd

-- +goose StatementBegin
-- Emit on every new review thread the SCM observer persists, so the broadcaster
-- can stream per-thread additions instead of waiting for a rolled-up review_decision flip.
CREATE TRIGGER pr_review_threads_cdc_insert
AFTER INSERT ON pr_review_threads
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (
        (SELECT s.project_id FROM pr p JOIN sessions s ON s.id = p.session_id WHERE p.url = NEW.pr_url),
        (SELECT session_id FROM pr WHERE url = NEW.pr_url),
        'pr_review_thread_added',
        json_object(
            'pr', NEW.pr_url,
            'thread', NEW.thread_id,
            'path', NEW.path,
            'line', NEW.line,
            'resolved', json(CASE WHEN NEW.resolved THEN 'true' ELSE 'false' END),
            'isBot', json(CASE WHEN NEW.is_bot THEN 'true' ELSE 'false' END)
        ),
        NEW.updated_at);
END;
-- +goose StatementEnd

-- +goose StatementBegin
-- Emit only on resolved <-> unresolved transitions. Other thread mutations
-- (semantic_hash refresh, line shifts) are captured by the slower review-decision
-- rollup so we don't flood CDC with no-op semantic-hash updates.
CREATE TRIGGER pr_review_threads_cdc_update
AFTER UPDATE ON pr_review_threads
WHEN OLD.resolved <> NEW.resolved
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (
        (SELECT s.project_id FROM pr p JOIN sessions s ON s.id = p.session_id WHERE p.url = NEW.pr_url),
        (SELECT session_id FROM pr WHERE url = NEW.pr_url),
        'pr_review_thread_resolved',
        json_object(
            'pr', NEW.pr_url,
            'thread', NEW.thread_id,
            'path', NEW.path,
            'line', NEW.line,
            'resolved', json(CASE WHEN NEW.resolved THEN 'true' ELSE 'false' END)
        ),
        NEW.updated_at);
END;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS pr_review_threads_cdc_update;
DROP TRIGGER IF EXISTS pr_review_threads_cdc_insert;
DROP INDEX IF EXISTS idx_change_log_project;
DROP TABLE change_log;
CREATE TABLE change_log (
    seq        INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id TEXT NOT NULL REFERENCES projects (id),
    session_id TEXT REFERENCES sessions (id),
    event_type TEXT NOT NULL
        CHECK (event_type IN ('session_created', 'session_updated', 'pr_created', 'pr_updated', 'pr_check_recorded')),
    payload    TEXT NOT NULL CHECK (json_valid(payload)),
    created_at TIMESTAMP NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_change_log_project ON change_log (project_id, seq);
DROP TABLE pr_review_threads;
ALTER TABLE pr_comment DROP COLUMN is_bot;
ALTER TABLE pr_comment DROP COLUMN url;
ALTER TABLE pr_comment DROP COLUMN thread_id;
ALTER TABLE pr_checks DROP COLUMN details;
ALTER TABLE pr_checks DROP COLUMN conclusion;
ALTER TABLE pr DROP COLUMN review_observed_at;
ALTER TABLE pr DROP COLUMN ci_observed_at;
ALTER TABLE pr DROP COLUMN observed_at;
ALTER TABLE pr DROP COLUMN review_hash;
ALTER TABLE pr DROP COLUMN ci_hash;
ALTER TABLE pr DROP COLUMN metadata_hash;
ALTER TABLE pr DROP COLUMN closed_at_provider;
ALTER TABLE pr DROP COLUMN merged_at_provider;
ALTER TABLE pr DROP COLUMN updated_at_provider;
ALTER TABLE pr DROP COLUMN created_at_provider;
ALTER TABLE pr DROP COLUMN html_url;
ALTER TABLE pr DROP COLUMN provider_merge_state_status;
ALTER TABLE pr DROP COLUMN provider_mergeable;
ALTER TABLE pr DROP COLUMN provider_state;
ALTER TABLE pr DROP COLUMN is_closed;
ALTER TABLE pr DROP COLUMN is_merged;
ALTER TABLE pr DROP COLUMN is_draft;
ALTER TABLE pr DROP COLUMN merge_commit_sha;
ALTER TABLE pr DROP COLUMN base_sha;
ALTER TABLE pr DROP COLUMN author;
ALTER TABLE pr DROP COLUMN changed_files;
ALTER TABLE pr DROP COLUMN deletions;
ALTER TABLE pr DROP COLUMN additions;
ALTER TABLE pr DROP COLUMN title;
ALTER TABLE pr DROP COLUMN head_sha;
ALTER TABLE pr DROP COLUMN target_branch;
ALTER TABLE pr DROP COLUMN source_branch;
ALTER TABLE pr DROP COLUMN repo;
ALTER TABLE pr DROP COLUMN host;
ALTER TABLE pr DROP COLUMN provider;
-- +goose StatementEnd
