-- +goose Up
-- preview_revision is a monotonic counter bumped on every `ao preview` call
-- (POST/DELETE /sessions/{id}/preview). The preview_url alone cannot tell a
-- repeated `ao preview <same-url>` from an unrelated session update replayed
-- over CDC, so the desktop browser panel could never refresh on a re-run. The
-- revision gives the renderer a per-command identity to key navigation on, so
-- re-running `ao preview` always re-navigates even when the URL is unchanged.
-- +goose StatementBegin
ALTER TABLE sessions ADD COLUMN preview_revision INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- Recreate the sessions update CDC trigger so a preview_revision bump also fans
-- out a session_updated event. Without this a same-URL `ao preview` re-run
-- would change only preview_revision/updated_at, which the prior trigger did
-- not watch, so the renderer never heard about the refresh.
-- +goose StatementBegin
DROP TRIGGER IF EXISTS sessions_cdc_update;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER sessions_cdc_update
AFTER UPDATE ON sessions
WHEN OLD.activity_state <> NEW.activity_state
    OR OLD.is_terminated <> NEW.is_terminated
    OR (OLD.first_signal_at IS NULL AND NEW.first_signal_at IS NOT NULL)
    OR OLD.preview_url <> NEW.preview_url
    OR OLD.preview_revision <> NEW.preview_revision
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NEW.project_id, NEW.id, 'session_updated',
        json_object('id', NEW.id, 'activity', NEW.activity_state, 'isTerminated', json(CASE WHEN NEW.is_terminated THEN 'true' ELSE 'false' END), 'previewUrl', NEW.preview_url, 'previewRevision', NEW.preview_revision),
        NEW.updated_at);
END;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS sessions_cdc_update;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER sessions_cdc_update
AFTER UPDATE ON sessions
WHEN OLD.activity_state <> NEW.activity_state
    OR OLD.is_terminated <> NEW.is_terminated
    OR (OLD.first_signal_at IS NULL AND NEW.first_signal_at IS NOT NULL)
    OR OLD.preview_url <> NEW.preview_url
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NEW.project_id, NEW.id, 'session_updated',
        json_object('id', NEW.id, 'activity', NEW.activity_state, 'isTerminated', json(CASE WHEN NEW.is_terminated THEN 'true' ELSE 'false' END), 'previewUrl', NEW.preview_url),
        NEW.updated_at);
END;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE sessions DROP COLUMN preview_revision;
-- +goose StatementEnd
