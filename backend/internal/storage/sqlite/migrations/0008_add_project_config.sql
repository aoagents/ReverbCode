-- Per-project configuration. A single nullable JSON column on projects holds the
-- typed ProjectConfig (agent settings, env, symlinks, post-create, rules, role
-- overrides, tracker/scm, …) AO resolves at spawn. NULL means unset; a non-NULL
-- value is a JSON object. One blob per project keeps the registry's "SQLite twin
-- of the YAML config" shape rather than splitting config into many columns.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE projects ADD COLUMN config TEXT;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE projects DROP COLUMN config;
-- +goose StatementEnd
