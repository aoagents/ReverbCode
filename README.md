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

The bind host is always `127.0.0.1`: the daemon is a loopback-only sidecar
and binding any other interface would be a security regression, so the host
is intentionally not env-configurable.

| Var | Default | Purpose |
|---|---|---|
| `AO_PORT` | `3001` | bind port; fails fast if taken |
| `AO_REQUEST_TIMEOUT` | `60s` | per-request timeout (Go duration) |
| `AO_SHUTDOWN_TIMEOUT` | `10s` | graceful-shutdown hard cap |
| `AO_RUN_FILE` | `<UserConfigDir>/agent-orchestrator/running.json` | PID + port handshake path |

### Test

```bash
cd backend
gofmt -l . && go build ./... && go vet ./... && go test -race ./...
```

## API contract (code-first OpenAPI)

The `/api/v1` contract is **code-first**: the Go request/response types are the
source of truth, and `backend/internal/httpd/apispec/openapi.yaml` plus the
frontend types (`frontend/src/api/schema.d.ts`) are **generated** from them.
Never hand-edit those files.

**To change or add a route:**

1. Edit the Go types (request/response structs + their `description`/`enum`/
   `default` tags); for a new route also add the handler in
   `controllers/projects.go` and its entry in `projectOperations()` in
   `apispec/build.go`.
2. Regenerate:
   ```bash
   cd backend && go generate ./...        # Go → openapi.yaml
   npm --prefix frontend run gen:api      # openapi.yaml → schema.d.ts
   ```
3. `go test ./...` — the drift and route↔spec parity tests fail if anything is
   out of sync.
4. Commit the Go change together with the regenerated `openapi.yaml` and
   `schema.d.ts`. CI's `gen-verify` job blocks merges on stale artifacts.

Full details and rationale: [`docs/api-contract.md`](docs/api-contract.md).

