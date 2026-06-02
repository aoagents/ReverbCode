# agent-orchestrator rewrite docs

The agent-orchestrator is being rebuilt as a long-running Go backend daemon
(`backend/`) plus an Electron + TypeScript frontend (`frontend/`). The backend
supervises coding-agent sessions and exposes daemon control, project/session
state, terminal streaming, and CDC/event infrastructure.

Start with [architecture.md](architecture.md) for the current backend model and
[cli/README.md](cli/README.md) for the CLI surface.

## Mental model

Persist durable facts, derive display status:

- session table: `activity_state`, `is_terminated`, identity, metadata
- PR tables: PR/CI/review facts
- derived read model: `service.Session` computes display status from session + PR facts
