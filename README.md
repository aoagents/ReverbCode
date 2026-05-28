# agent-orchestrator

Rewrite of the agent-orchestrator: a long-running Go backend daemon (`backend/`)
paired with an Electron + TypeScript frontend (`frontend/`).

See [`docs/`](docs/README.md) for architecture and status — start with the
Lifecycle Manager + Session Manager lane in [`docs/architecture.md`](docs/architecture.md).

## Backend daemon

The Go binary in [`backend/`](backend/) is the HTTP daemon — a loopback-only
sidecar the Electron supervisor will spawn (Phase 1c). Phase 1a landed the
skeleton: chi router, middleware stack (recoverer → request-id → logger →
real-ip), `/healthz` + `/readyz`, atomic `running.json` PID/port handshake,
graceful shutdown on SIGINT/SIGTERM.

### Run

```bash
cd backend
go run .                          # binds 127.0.0.1:3001 with all defaults
AO_PORT=3019 go run .             # override per invocation
```

Health check:

```bash
curl localhost:3001/healthz       # {"status":"ok"}
curl localhost:3001/readyz        # {"status":"ready"}
```

### Configuration (env only)

| Var | Default | Purpose |
|---|---|---|
| `AO_HOST` | `127.0.0.1` | bind host — keep loopback |
| `AO_PORT` | `3001` | bind port; fails fast if taken |
| `AO_REQUEST_TIMEOUT` | `60s` | per-request timeout (Go duration) |
| `AO_SHUTDOWN_TIMEOUT` | `10s` | graceful-shutdown hard cap |
| `AO_RUN_FILE` | `<UserConfigDir>/agent-orchestrator/running.json` | PID + port handshake path |

### Test

```bash
cd backend
gofmt -l . && go build ./... && go vet ./... && go test -race ./...
```

