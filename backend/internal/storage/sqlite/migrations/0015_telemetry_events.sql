-- +goose Up
-- +goose StatementBegin
CREATE TABLE telemetry_event (
    id TEXT PRIMARY KEY,
    occurred_at TIMESTAMP NOT NULL,
    name TEXT NOT NULL,
    source TEXT NOT NULL,
    level TEXT NOT NULL CHECK (level IN ('debug', 'info', 'warn', 'error')),
    project_id TEXT,
    session_id TEXT,
    request_id TEXT NOT NULL DEFAULT '',
    payload_json TEXT NOT NULL
);

CREATE INDEX idx_telemetry_event_occurred_at
    ON telemetry_event(occurred_at DESC);

CREATE INDEX idx_telemetry_event_name
    ON telemetry_event(name, occurred_at DESC);

CREATE INDEX idx_telemetry_event_project
    ON telemetry_event(project_id, occurred_at DESC);

CREATE INDEX idx_telemetry_event_session
    ON telemetry_event(session_id, occurred_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_telemetry_event_session;
DROP INDEX IF EXISTS idx_telemetry_event_project;
DROP INDEX IF EXISTS idx_telemetry_event_name;
DROP INDEX IF EXISTS idx_telemetry_event_occurred_at;
DROP TABLE IF EXISTS telemetry_event;
-- +goose StatementEnd
