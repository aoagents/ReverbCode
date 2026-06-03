# agent-orchestrator

Rewrite of the agent-orchestrator: a long-running Go backend daemon (`backend/`)
paired with a placeholder Electron + TypeScript frontend shell (`frontend/`).

See [`docs/`](docs/README.md) for architecture and status — start with the
Lifecycle Manager + Session Service lane in [`docs/architecture.md`](docs/architecture.md).

## Backend daemon

The Go backend now has a Cobra-based `ao` CLI in [`backend/cmd/ao`](backend/cmd/ao).
The CLI controls the HTTP daemon — a loopback-only sidecar the Electron
supervisor will also use. The daemon skeleton includes the chi router,
middleware stack (recoverer → request-id → logger → real-ip), `/healthz` +
`/readyz`, atomic `running.json` PID/port handshake, graceful shutdown on
SIGINT/SIGTERM, SQLite storage, CDC polling, and lifecycle/reaper wiring.

### Run

```bash
cd backend
go run ./cmd/ao start             # start the daemon and wait for readiness
go run ./cmd/ao status            # inspect PID/port/health/readiness
go run ./cmd/ao stop              # gracefully stop the daemon
go run ./cmd/ao daemon            # internal daemon entrypoint

go run .                          # compatibility wrapper; starts the daemon
AO_PORT=3019 go run ./cmd/ao start # override per invocation
```

Health check:

```bash
curl localhost:3001/healthz       # includes status/service/pid
curl localhost:3001/readyz        # includes status/service/pid
```

### Configuration (env only)

The bind host is always `127.0.0.1`: the daemon is a loopback-only sidecar
and binding any other interface would be a security regression, so the host
is intentionally not env-configurable.

| Var | Default | Purpose |
|---|---|---|
| `AO_PORT` | `3001` | bind port; fails fast if taken |
| `AO_REQUEST_TIMEOUT` | `60s` | per-request timeout (Go duration) |
| `AO_SHUTDOWN_TIMEOUT` | `10s` | graceful-shutdown hard cap |
| `AO_RUN_FILE` | `<UserConfigDir>/agent-orchestrator/running.json` | PID + port handshake path |
| `AO_DATA_DIR` | `<UserConfigDir>/agent-orchestrator/data` | SQLite DB, WAL files, and managed state |

### Test

```bash
npm run lint
# optional deeper backend pass:
cd backend && go test -race ./...
```
