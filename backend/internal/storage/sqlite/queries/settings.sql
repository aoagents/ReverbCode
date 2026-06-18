-- name: GetAgentDefaults :one
SELECT default_worker_agent, default_orchestrator_agent
FROM app_settings
WHERE id = 1;

-- name: UpsertAgentDefaults :exec
INSERT INTO app_settings (id, default_worker_agent, default_orchestrator_agent)
VALUES (1, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    default_worker_agent = excluded.default_worker_agent,
    default_orchestrator_agent = excluded.default_orchestrator_agent;
