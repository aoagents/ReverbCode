# AO CLI

The `ao` CLI is a thin Go/Cobra client for the local Agent Orchestrator daemon.
It starts, discovers, inspects, and stops the daemon through the loopback HTTP
surface and the `running.json` handshake. It must not open SQLite directly or
call runtime, workspace, tracker, or agent adapters in-process.

## Current commands

| Command | Purpose |
|---|---|
| `ao start` | Start the daemon in the background and wait for `/readyz`. |
| `ao status` | Report daemon state from `running.json`, process liveness, `/healthz`, and `/readyz`. |
| `ao status --json` | Emit the same daemon state as machine-readable JSON. |
| `ao stop` | Gracefully stop the daemon via loopback `POST /shutdown` after verifying daemon identity. |
| `ao doctor` | Check config, data directory, DB-file presence, daemon state, `git`, and optional `zellij`. |
| `ao doctor --json` | Emit doctor checks as JSON. |
| `ao completion <shell>` | Generate completions for `bash`, `zsh`, `fish`, or `powershell`. |
| `ao version` / `ao --version` | Print build metadata. |
| `ao daemon` | Hidden internal daemon entrypoint used by `ao start`. |

`go run .` in `backend/` remains a compatibility wrapper around the daemon.

## Configuration

The CLI and daemon share the same environment-driven config:

| Var | Default | Purpose |
|---|---|---|
| `AO_PORT` | `3001` | Loopback daemon port. |
| `AO_RUN_FILE` | `<UserConfigDir>/agent-orchestrator/running.json` | PID/port handshake. |
| `AO_DATA_DIR` | `<UserConfigDir>/agent-orchestrator/data` | SQLite data directory. |
| `AO_REQUEST_TIMEOUT` | `60s` | REST request timeout. |
| `AO_SHUTDOWN_TIMEOUT` | `10s` | Graceful shutdown cap. |

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

## Product commands not present yet

The backend has project, session, lifecycle, terminal, and CDC building blocks,
but the public CLI currently exposes only daemon-control commands. Add product
commands only when a daemon HTTP route owns the corresponding mutation/read:

- `ao project ...` should call project HTTP routes.
- `ao spawn`, `ao session ...`, and `ao send` should call session/messaging HTTP routes.
- `ao events ...` should call CDC/event HTTP routes.

Do not port old in-process TypeScript CLI behavior that mixed command handling
with storage and runtime implementation details.
