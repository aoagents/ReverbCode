-- +goose Up
-- +goose StatementBegin

-- projects is the durable registry of repos AO manages (the SQLite twin of the
-- YAML config). id is a short human/LLM-friendly slug (mer, ao) with a numeric
-- suffix on collision (ao, ao1, ao2). Soft-delete via archived_at keeps the row
-- so a session's project_id always resolves.
CREATE TABLE projects (
    id              TEXT PRIMARY KEY,
    path            TEXT NOT NULL,
    repo_origin_url TEXT NOT NULL DEFAULT '',
    display_name    TEXT NOT NULL DEFAULT '',
    registered_at   TIMESTAMP NOT NULL,
    archived_at     TIMESTAMP
);

-- sessions is the durable session fact row. id is "{project_id}-{num}"
-- (e.g. mer-1), so every inbound FK is single-column. num is the per-project
-- counter. The only persisted status-like facts are activity_state and
-- is_terminated; display status is derived on read from this row plus PR facts.
CREATE TABLE sessions (
    id                      TEXT PRIMARY KEY,
    project_id              TEXT NOT NULL REFERENCES projects (id),
    num                     INTEGER NOT NULL,
    issue_id                TEXT NOT NULL DEFAULT '',
    kind                    TEXT NOT NULL DEFAULT 'worker'
        CHECK (kind IN ('worker', 'orchestrator')),
    harness                 TEXT NOT NULL DEFAULT ''
        CHECK (harness IN ('', 'claude-code', 'codex', 'aider', 'opencode')),

    activity_state          TEXT NOT NULL DEFAULT 'idle'
        CHECK (activity_state IN ('active', 'idle', 'waiting_input', 'blocked', 'exited')),
    activity_last_at        TIMESTAMP NOT NULL,
    activity_source         TEXT NOT NULL DEFAULT 'none'
        CHECK (activity_source IN ('native', 'terminal', 'hook', 'runtime', 'none')),
    is_terminated           BOOLEAN NOT NULL DEFAULT FALSE,

    branch                  TEXT NOT NULL DEFAULT '',
    workspace_path          TEXT NOT NULL DEFAULT '',
    runtime_handle_id       TEXT NOT NULL DEFAULT '',
    agent_session_id        TEXT NOT NULL DEFAULT '',
    prompt                  TEXT NOT NULL DEFAULT '',

    created_at              TIMESTAMP NOT NULL,
    updated_at              TIMESTAMP NOT NULL,

    UNIQUE (project_id, num)
);
CREATE INDEX idx_sessions_project ON sessions (project_id);

-- pr holds PR facts keyed by the normalized PR URL. One session can own many PRs
-- (session_id FK), but a PR belongs to one session (enforced at runtime). ci_state
-- is the rolled-up status; the per-check history lives in pr_checks.
CREATE TABLE pr (
    url             TEXT PRIMARY KEY,
    session_id      TEXT NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    number          INTEGER NOT NULL DEFAULT 0,
    pr_state        TEXT NOT NULL DEFAULT 'open'
        CHECK (pr_state IN ('draft', 'open', 'merged', 'closed')),
    review_decision TEXT NOT NULL DEFAULT 'none'
        CHECK (review_decision IN ('none', 'approved', 'changes_requested', 'review_required')),
    ci_state        TEXT NOT NULL DEFAULT 'unknown'
        CHECK (ci_state IN ('unknown', 'pending', 'passing', 'failing')),
    mergeability    TEXT NOT NULL DEFAULT 'unknown'
        CHECK (mergeability IN ('unknown', 'mergeable', 'conflicting', 'blocked', 'unstable')),
    updated_at      TIMESTAMP NOT NULL
);
CREATE INDEX idx_pr_session ON pr (session_id);

-- pr_checks is CI run history: one row per (PR, check, commit). Re-polling the
-- same commit upserts the same row.
CREATE TABLE pr_checks (
    pr_url      TEXT NOT NULL REFERENCES pr (url) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    commit_hash TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'unknown'
        CHECK (status IN ('unknown', 'queued', 'in_progress', 'passed', 'failed', 'skipped', 'cancelled')),
    url         TEXT NOT NULL DEFAULT '',
    log_tail    TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMP NOT NULL,
    PRIMARY KEY (pr_url, name, commit_hash)
);
CREATE INDEX idx_pr_checks_lookup ON pr_checks (pr_url, name, created_at);

-- pr_comment holds review comments, persisted so a session page does not wait on
-- GitHub. Cascades from pr.
CREATE TABLE pr_comment (
    pr_url     TEXT NOT NULL REFERENCES pr (url) ON DELETE CASCADE,
    comment_id TEXT NOT NULL,
    author     TEXT NOT NULL DEFAULT '',
    file       TEXT NOT NULL DEFAULT '',
    line       INTEGER NOT NULL DEFAULT 0,
    body       TEXT NOT NULL DEFAULT '',
    resolved   INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP NOT NULL,
    PRIMARY KEY (pr_url, comment_id)
);

-- change_log is the durable, append-only CDC event log. seq is the monotonic
-- ordering + idempotency key. Rows are written by TRIGGERS on the user-visible
-- tables (DB-native capture, atomic with the change) — never by application
-- emit-code. project_id is required, session_id is nullable (project-level events
-- have no session). The log is immutable (no published flag); consumers track
-- their own offset (SSE Last-Event-ID).
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

-- +goose StatementEnd

-- CDC capture triggers. Each is its own goose statement (the trigger body holds
-- semicolons). They write change_log atomically with the originating change, so
-- the application never emits events — it just writes sessions/pr/pr_checks.

-- +goose StatementBegin
CREATE TRIGGER sessions_cdc_insert
AFTER INSERT ON sessions
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NEW.project_id, NEW.id, 'session_created',
        json_object('id', NEW.id, 'activity', NEW.activity_state, 'isTerminated', json(CASE WHEN NEW.is_terminated THEN 'true' ELSE 'false' END)),
        NEW.updated_at);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER sessions_cdc_update
AFTER UPDATE ON sessions
WHEN OLD.activity_state <> NEW.activity_state
    OR OLD.is_terminated <> NEW.is_terminated
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NEW.project_id, NEW.id, 'session_updated',
        json_object('id', NEW.id, 'activity', NEW.activity_state, 'isTerminated', json(CASE WHEN NEW.is_terminated THEN 'true' ELSE 'false' END)),
        NEW.updated_at);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER pr_cdc_insert
AFTER INSERT ON pr
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES ((SELECT project_id FROM sessions WHERE id = NEW.session_id), NEW.session_id, 'pr_created',
        json_object('url', NEW.url, 'session', NEW.session_id, 'state', NEW.pr_state,
                    'ci', NEW.ci_state, 'review', NEW.review_decision, 'mergeability', NEW.mergeability),
        NEW.updated_at);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER pr_cdc_update
AFTER UPDATE ON pr
WHEN OLD.pr_state <> NEW.pr_state
    OR OLD.ci_state <> NEW.ci_state
    OR OLD.review_decision <> NEW.review_decision
    OR OLD.mergeability <> NEW.mergeability
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES ((SELECT project_id FROM sessions WHERE id = NEW.session_id), NEW.session_id, 'pr_updated',
        json_object('url', NEW.url, 'session', NEW.session_id, 'state', NEW.pr_state,
                    'ci', NEW.ci_state, 'review', NEW.review_decision, 'mergeability', NEW.mergeability),
        NEW.updated_at);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER pr_checks_cdc_insert
AFTER INSERT ON pr_checks
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (
        (SELECT s.project_id FROM pr p JOIN sessions s ON s.id = p.session_id WHERE p.url = NEW.pr_url),
        (SELECT session_id FROM pr WHERE url = NEW.pr_url),
        'pr_check_recorded',
        json_object('pr', NEW.pr_url, 'name', NEW.name, 'commit', NEW.commit_hash, 'status', NEW.status),
        NEW.created_at);
END;
-- +goose StatementEnd

-- A re-polled check can change status on the same commit (in_progress -> failed)
-- via UpsertPRCheck's ON CONFLICT DO UPDATE. Without this trigger that status
-- transition would update the row silently, so CDC consumers would never see it.
-- Guarded on the status so a no-op re-poll emits nothing.
-- +goose StatementBegin
CREATE TRIGGER pr_checks_cdc_update
AFTER UPDATE ON pr_checks
WHEN OLD.status <> NEW.status
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (
        (SELECT s.project_id FROM pr p JOIN sessions s ON s.id = p.session_id WHERE p.url = NEW.pr_url),
        (SELECT session_id FROM pr WHERE url = NEW.pr_url),
        'pr_check_recorded',
        json_object('pr', NEW.pr_url, 'name', NEW.name, 'commit', NEW.commit_hash, 'status', NEW.status),
        datetime('now'));
END;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE change_log;
DROP TABLE pr_comment;
DROP TABLE pr_checks;
DROP TABLE pr;
DROP TABLE sessions;
DROP TABLE projects;
-- +goose StatementEnd
