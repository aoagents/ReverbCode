-- +goose Up
-- +goose StatementBegin

CREATE TABLE app_settings (
    id                         INTEGER PRIMARY KEY CHECK (id = 1),
    default_worker_agent       TEXT NOT NULL DEFAULT ''
        CHECK (default_worker_agent IN ('', 'claude-code', 'codex', 'aider', 'opencode', 'grok', 'droid', 'amp', 'agy', 'crush', 'cursor', 'qwen', 'copilot', 'goose', 'auggie', 'continue', 'devin', 'cline', 'kimi', 'kiro', 'kilocode', 'vibe', 'pi', 'autohand')),
    default_orchestrator_agent TEXT NOT NULL DEFAULT ''
        CHECK (default_orchestrator_agent IN ('', 'claude-code', 'codex', 'aider', 'opencode', 'grok', 'droid', 'amp', 'agy', 'crush', 'cursor', 'qwen', 'copilot', 'goose', 'auggie', 'continue', 'devin', 'cline', 'kimi', 'kiro', 'kilocode', 'vibe', 'pi', 'autohand'))
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE app_settings;
-- +goose StatementEnd
