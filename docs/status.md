# agent-orchestrator status

Current main contains the Go backend daemon, Cobra CLI foundation, SQLite store,
CDC poller/broadcaster, lifecycle/session managers, terminal mux, project API
controller/manager work, runtime/workspace/tracker adapters, and CDC-backed event rows.

## Build & test

```bash
npm run lint
```

## Current shape

- CLI: `ao start`, `status`, `stop`, `doctor`, `completion`, `version`, and the
  hidden daemon entrypoint.
- Session facts: `activity_state` and `is_terminated`; display status is derived
  from those plus PR facts.
- SQLite: migrations create projects, sessions, PR/check/comment, and `change_log` tables.
- CDC: DB triggers append to `change_log`; the poller broadcasts live events.
- Session Manager: spawn/kill/restore/list/get/send/cleanup over runtime,
  workspace, agent, store, messenger, and lifecycle ports.
- Agent harness: production adapter is still a loud stub in daemon wiring, so
  spawn/restore should fail clearly until harness adapters are implemented.

## Next integration work

- Wire production agent adapters.
- Finish project/session HTTP routes and CLI product commands.
- Add SSE/event read endpoints over the CDC log.
