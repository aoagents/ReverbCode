-- +goose Up
-- first_signal_at records when the FIRST agent hook callback arrived for a
-- session: raw signal receipt, independent of the derived activity state.
-- NULL means no hook has ever reported for the current spawn/restore; the
-- session service derives the "no_signal" display status from it so a broken
-- hook pipeline (agent upgrade, PATH problem, blocked interactive prompt)
-- surfaces as "no activity signal" instead of a confident "idle".
--
-- Backfill existing rows from activity_last_at: sessions created before this
-- column are treated as having signaled so an upgrade doesn't flip every
-- historical session to no_signal.
-- +goose StatementBegin
ALTER TABLE sessions ADD COLUMN first_signal_at TIMESTAMP;
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE sessions SET first_signal_at = activity_last_at;
-- +goose StatementEnd

-- Recreate the sessions update CDC trigger so the first hook receipt also
-- fans out a session_updated event: hook deliveries are best-effort, so the
-- first signal to arrive may repeat the seeded activity state (a lost "active"
-- POST followed by a Stop hook landing idle on the idle-seeded row), and
-- without this clause the dashboard would keep showing no_signal until the
-- next real state change.
-- +goose StatementBegin
DROP TRIGGER IF EXISTS sessions_cdc_update;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER sessions_cdc_update
AFTER UPDATE ON sessions
WHEN OLD.activity_state <> NEW.activity_state
    OR OLD.is_terminated <> NEW.is_terminated
    OR (OLD.first_signal_at IS NULL AND NEW.first_signal_at IS NOT NULL)
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NEW.project_id, NEW.id, 'session_updated',
        json_object('id', NEW.id, 'activity', NEW.activity_state, 'isTerminated', json(CASE WHEN NEW.is_terminated THEN 'true' ELSE 'false' END)),
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
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NEW.project_id, NEW.id, 'session_updated',
        json_object('id', NEW.id, 'activity', NEW.activity_state, 'isTerminated', json(CASE WHEN NEW.is_terminated THEN 'true' ELSE 'false' END)),
        NEW.updated_at);
END;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE sessions DROP COLUMN first_signal_at;
-- +goose StatementEnd
