-- +goose Up
-- +goose StatementBegin
ALTER TABLE sessions ADD COLUMN display_name TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE sessions DROP COLUMN display_name;
-- +goose StatementEnd
