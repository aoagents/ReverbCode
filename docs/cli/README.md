# AO CLI

The `ao` CLI is a thin Go/Cobra client for the local Agent Orchestrator daemon.
It starts, discovers, inspects, and stops the daemon through the loopback HTTP
surface and the `running.json` handshake. It must not open SQLite directly or
call runtime, workspace, tracker, or agent adapters in-process.

## Current commands

Every product command resolves to a daemon HTTP route. Run `ao <command>
--help` for the authoritative flag shape.

### Daemon control

| Command                       | Purpose                                                                                     |
| ----------------------------- | ------------------------------------------------------------------------------------------- |
| `ao start`                    | Start the daemon in the background and wait for `/readyz`.                                  |
| `ao stop`                     | Gracefully stop the daemon via loopback `POST /shutdown` after verifying daemon identity.   |
| `ao status` / `--json`        | Report daemon state from `running.json`, process liveness, `/healthz`, and `/readyz`.       |
| `ao doctor` / `--json`        | Check config, data directory, DB-file presence, daemon state, `git`, and optional `zellij`. |
| `ao completion <shell>`       | Generate completions for `bash`, `zsh`, `fish`, or `powershell`.                            |
| `ao version` / `ao --version` | Print build metadata.                                                                       |
| `ao daemon`                   | Hidden internal daemon entrypoint used by `ao start`.                                       |

### Product commands

| Command                             | Daemon route                                   |
| ----------------------------------- | ---------------------------------------------- |
| `ao project add`                    | `POST /api/v1/projects`                        |
| `ao project ls`                     | `GET /api/v1/projects`                         |
| `ao project get <id>`               | `GET /api/v1/projects/{id}`                    |
| `ao project set-config <id>`        | `PUT /api/v1/projects/{id}/config`             |
| `ao project rm <id>`                | `DELETE /api/v1/projects/{id}`                 |
| `ao spawn`                          | `POST /api/v1/sessions`                        |
| `ao session ls`                     | `GET /api/v1/sessions`                         |
| `ao session get <id>`               | `GET /api/v1/sessions/{id}`                    |
| `ao session kill <id>`              | `POST /api/v1/sessions/{id}/kill`              |
| `ao session restore <id>`           | `POST /api/v1/sessions/{id}/restore`           |
| `ao session rename <id> <name>`     | `PATCH /api/v1/sessions/{id}`                  |
| `ao session cleanup`                | `POST /api/v1/sessions/cleanup`                |
| `ao session claim-pr <id> <pr-ref>` | `POST /api/v1/sessions/{id}/pr/claim`          |
| `ao orchestrator ls`                | `GET /api/v1/orchestrators`                    |
| `ao send`                           | `POST /api/v1/sessions/{id}/send`              |
| `ao hooks <agent> <event>`          | `POST /api/v1/sessions/{id}/activity` (hidden) |

`go run .` in `backend/` remains a compatibility wrapper around the daemon.

PR and review actions (merge, resolve-comments, review execute/send) are
HTTP-only today and driven by the frontend; there are no `ao pr` / `ao review`
commands yet.

## Configuration

The CLI and daemon share the same environment-driven config:

| Var                   | Default                                           | Purpose                |
| --------------------- | ------------------------------------------------- | ---------------------- |
| `AO_PORT`             | `3001`                                            | Loopback daemon port.  |
| `AO_RUN_FILE`         | `<UserConfigDir>/agent-orchestrator/running.json` | PID/port handshake.    |
| `AO_DATA_DIR`         | `<UserConfigDir>/agent-orchestrator/data`         | SQLite data directory. |
| `AO_REQUEST_TIMEOUT`  | `60s`                                             | REST request timeout.  |
| `AO_SHUTDOWN_TIMEOUT` | `10s`                                             | Graceful shutdown cap. |

The daemon always binds `127.0.0.1`.

## Manual smoke test

```bash
cd backend
go build -o /tmp/ao ./cmd/ao

tmp=$(mktemp -d)
export AO_RUN_FILE="$tmp/running.json"
export AO_DATA_DIR="$tmp/data"
export AO_PORT=3037

/tmp/ao status --json
/tmp/ao doctor
/tmp/ao start
/tmp/ao status --json
/tmp/ao stop
/tmp/ao status --json
rm -rf "$tmp"
```

## Adding new commands

Add a product command only when a daemon HTTP route owns the corresponding
mutation/read; the CLI must call that route rather than reimplementing daemon
behavior. Commands not yet exposed but with backend routes in place include
`ao events ...` (over the CDC/SSE endpoint) and CLI parity for PR/review
actions.

Do not port old in-process TypeScript CLI behavior that mixed command handling
with storage and runtime implementation details.
