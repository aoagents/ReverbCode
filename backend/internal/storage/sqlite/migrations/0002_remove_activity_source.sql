-- +goose Up
-- +goose StatementBegin
UPDATE sessions SET activity_state = 'waiting_input' WHERE activity_state = 'blocked';
ALTER TABLE sessions DROP COLUMN activity_source;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE sessions ADD COLUMN activity_source TEXT NOT NULL DEFAULT 'none'
    CHECK (activity_source IN ('native', 'terminal', 'hook', 'runtime', 'none'));
-- +goose StatementEnd
