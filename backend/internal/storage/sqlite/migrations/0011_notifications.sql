-- +goose Up
-- +goose StatementBegin
CREATE TABLE notifications (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    pr_url TEXT NOT NULL DEFAULT '',
    type TEXT NOT NULL CHECK (
        type IN (
            'needs_input',
            'ready_to_merge',
            'pr_merged',
            'pr_closed_unmerged'
        )
    ),
    title TEXT NOT NULL,
    body TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'unread' CHECK (status IN ('read', 'unread')),
    created_at TIMESTAMP NOT NULL
);

CREATE INDEX idx_notifications_unread
    ON notifications(status, created_at DESC);

CREATE INDEX idx_notifications_project_unread
    ON notifications(project_id, status, created_at DESC);

CREATE INDEX idx_notifications_session
    ON notifications(session_id, created_at DESC);

CREATE UNIQUE INDEX idx_notifications_unread_dedupe
    ON notifications(session_id, type, pr_url)
    WHERE status = 'unread';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_notifications_unread_dedupe;
DROP INDEX IF EXISTS idx_notifications_session;
DROP INDEX IF EXISTS idx_notifications_project_unread;
DROP INDEX IF EXISTS idx_notifications_unread;
DROP TABLE IF EXISTS notifications;
-- +goose StatementEnd
