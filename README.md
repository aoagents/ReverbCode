# ReverbCode

The orchestration layer for parallel AI coding agents. ReverbCode is a
Go-backed daemon that supervises many coding-agent sessions at once, each in
its own `git worktree`, and routes the feedback they need (CI failures, review
comments, merge conflicts) back to the right agent automatically. It ships with
an `ao` CLI and an Electron supervisor that both drive the same daemon over
loopback.

The Go module and packages remain `agent-orchestrator`; "ReverbCode" is the
public name.

See [`docs/architecture.md`](docs/architecture.md) for the backend mental model
and [`AGENTS.md`](AGENTS.md) for the contributor / worker contract. For current
progress (what's shipped vs. in flight) see [`docs/STATUS.md`](docs/STATUS.md).

## What it does

- **Agent-agnostic.** A 23-adapter platform under
  `backend/internal/adapters/agent/` (`claude-code`, `codex`, `cursor`,
  `opencode`, `aider`, `amp`, `goose`, `copilot`, `grok`, `qwen`, `kimi`,
  `crush`, `cline`, `droid`, `devin`, `auggie`, `continue`, `kiro`, `kilocode`,
  and more), registered through a shared registry with common
  activity-dispatch / hook utilities. Worker and orchestrator defaults are set
  per project.
- **Isolated workspaces.** Worker and orchestrator sessions spawn into their own
  `git worktree` (`backend/internal/adapters/workspace/gitworktree/`), launched
  inside a `zellij` runtime adapter (`backend/internal/adapters/runtime/`) so
  every session has its own attachable terminal.
- **Live PR observation.** The provider-neutral SCM observer
  (`backend/internal/observe/scm/`) polls each session's PR with ETag guards and
  semantic diffing, tracking CI/check runs and review threads, and feeds those
  facts into the lifecycle manager, which sends the owning agent nudges for CI
  failures, review feedback, and merge conflicts. GitHub is the implemented
  provider today.
- **Durable facts, derived status.** The SQLite store
  (`backend/internal/storage/sqlite/`) persists a small set of session facts
  plus PR/check/comment rows; display status is computed at read time, never
  stored. DB triggers append every user-visible change to `change_log`, and a
  CDC poller/broadcaster (`backend/internal/cdc/`) feeds in-process subscribers
  and an SSE replay endpoint.
- **Loopback-only daemon.** The HTTP daemon (`backend/internal/httpd`) controls
  projects, sessions, orchestrators, and hook callbacks over `127.0.0.1` with no
  auth, CORS, or TLS by design.
- **Lifecycle manager + reaper** (`backend/internal/lifecycle/`,
  `backend/internal/observe/reaper/`) reduce runtime/activity/PR observations
  into the durable session state and reclaim dead sessions.

## How it works

1. Register a local git repo as a project (`ao project add`).
2. Spawn a worker session (`ao spawn`), or an orchestrator that fans work out
   across sessions. Each session gets its own `git worktree` and a `zellij`
   pane.
3. The agent develops, tests, and opens a PR from inside its worktree.
4. The SCM observer watches that PR and routes feedback back to the agent: a CI
   failure, a requested change, or a merge conflict becomes a nudge to the agent
   that owns the PR.
5. You inspect, attach a terminal, and merge from the CLI or the Electron app;
   human attention is needed only where the loop can't resolve on its own.

## Extensibility

The backend is organized around inbound/outbound port contracts
(`backend/internal/ports/`) with swappable adapters under
`backend/internal/adapters/`:

| Port      | Implemented adapters                          |
| --------- | --------------------------------------------- |
| Agent     | 23 harnesses (see above)                      |
| Runtime   | `zellij`                                      |
| Workspace | `git worktree`                                |
| SCM       | GitHub                                        |
| Tracker   | GitHub (adapter present; no runtime loop yet) |
| Reviewer  | `claude-code`                                 |
| Notifier  | port defined; no shipped adapter yet          |

See [`docs/STATUS.md`](docs/STATUS.md) for which lanes are live at runtime.

## Quick start

Requirements: Go 1.25+, [`zellij`](https://zellij.dev/) on `PATH` for the
runtime adapter, and `gh` (or `GITHUB_TOKEN`) if you want the SCM observer to
authenticate against GitHub. The SQLite driver is the pure-Go
`modernc.org/sqlite` — no system SQLite library is required.

```bash
cd backend
go build -o /tmp/ao ./cmd/ao

# Start the daemon and wait for /readyz.
/tmp/ao start

# Register a local git repo as a project. The id defaults to the lowercased
# base of --path; pass --id explicitly when the directory name doesn't match.
/tmp/ao project add --path /path/to/your/repo --id your-repo --name your-repo \
  --worker-agent codex --orchestrator-agent codex

# Spawn a worker session running the project's worker agent.
/tmp/ao spawn --project your-repo --prompt "Refactor the auth module"

# Inspect what's running.
/tmp/ao status
/tmp/ao session ls
```

### Electron app (dev)

The desktop supervisor lives under `frontend/` and is started separately:

```bash
cd frontend
npm install
npm run dev   # electron-forge start
```

Heads-up: `npm run dev` does **not** start the daemon for you. Start it first
(`ao start`, see above) — the renderer attaches to the running daemon over
loopback (`127.0.0.1:3001` by default, the `AO_PORT` from the table below).
Without a daemon the app opens but shows its daemon-not-ready state.

For renderer-only UI work without the Electron shell, use
`npm run dev:web` (Vite in a regular browser).

## CLI surface

The CLI is intentionally thin: every product command resolves to a daemon HTTP
route. Run `ao <command> --help` for the authoritative flag shape; the table
below groups what's on `main` today.

| Lane         | Command                              | Purpose                                                      |
| ------------ | ------------------------------------ | ------------------------------------------------------------ |
| Daemon       | `ao start`                           | Start the daemon in the background and wait for `/readyz`.   |
| Daemon       | `ao stop`                            | Graceful shutdown via loopback `POST /shutdown`.             |
| Daemon       | `ao status`                          | Report PID/port/health/readiness from `running.json`.        |
| Daemon       | `ao daemon`                          | Hidden internal entrypoint used by `ao start`.               |
| Project      | `ao project add`                     | Register a local git repo as a project.                      |
| Project      | `ao project ls`                      | List registered projects.                                    |
| Project      | `ao project get <id>`                | Fetch one project.                                           |
| Project      | `ao project set-config <id>`         | Update per-project config.                                   |
| Project      | `ao project rm <id>`                 | Remove a project.                                            |
| Session      | `ao spawn`                           | Spawn a worker session in a registered project.              |
| Session      | `ao session ls`                      | List sessions (filter by project, include terminated).       |
| Session      | `ao session get <id>`                | Fetch one session.                                           |
| Session      | `ao session kill <id>`               | Terminate a session.                                         |
| Session      | `ao session rename <id> <name>`      | Rename a session.                                            |
| Session      | `ao session restore <id>`            | Relaunch a terminated session.                               |
| Session      | `ao session cleanup`                 | Reclaim eligible workspaces for terminated sessions.         |
| Session      | `ao session claim-pr <session> <pr>` | Attach an existing PR to a session.                          |
| Orchestrator | `ao orchestrator ls`                 | List orchestrator sessions.                                  |
| Messaging    | `ao send`                            | Send a message to a running agent session.                   |
| Utility      | `ao doctor`                          | Local health checks (config, data dir, DB, `git`, `zellij`). |
| Utility      | `ao completion <shell>`              | Generate bash/zsh/fish/powershell completions.               |
| Utility      | `ao version`                         | Print build metadata.                                        |
| Internal     | `ao hooks <agent> <event>`           | Hidden adapter hook callback.                                |

See [`docs/cli/`](docs/cli/) for the daemon-control intent and command shape.

## Configuration

All configuration is env-driven; the daemon takes no config file. The bind
host is hard-coded to `127.0.0.1` — the daemon has no auth, CORS, or TLS, and
exposing it beyond loopback would be a security regression.

| Var                   | Default                                           | Purpose                                                                     |
| --------------------- | ------------------------------------------------- | --------------------------------------------------------------------------- |
| `AO_PORT`             | `3001`                                            | Bind port; daemon fails fast if taken.                                      |
| `AO_REQUEST_TIMEOUT`  | `60s`                                             | Per-request timeout (Go duration).                                          |
| `AO_SHUTDOWN_TIMEOUT` | `10s`                                             | Graceful-shutdown hard cap.                                                 |
| `AO_RUN_FILE`         | `<UserConfigDir>/agent-orchestrator/running.json` | PID + port handshake path.                                                  |
| `AO_DATA_DIR`         | `<UserConfigDir>/agent-orchestrator/data`         | SQLite DB, WAL files, managed state.                                        |
| `AO_AGENT`            | `claude-code`                                     | Compatibility agent adapter id validated at daemon startup.                 |
| `AO_SESSION_ID`       | _(unset)_                                         | Set inside spawned sessions; read by `ao send` and `ao hooks`.              |
| `GITHUB_TOKEN`        | _(unset)_                                         | Used by the GitHub SCM and tracker adapters. Falls back to `gh auth token`. |

Health check:

```bash
curl localhost:3001/healthz
curl localhost:3001/readyz
```

## Architecture

The daemon is a long-running supervisor. Adapters observe external facts (PR
state, agent activity, runtime liveness); the lifecycle manager reduces those
into a small set of durable session facts (`activity_state`, `is_terminated`,
PR rows). Display status is _derived_ from those facts at read time — it is
never stored. SQLite triggers append every user-visible change to `change_log`,
and the CDC poller broadcasts those events to in-process subscribers and an
SSE stream.

Full mental model and load-bearing rules: [`docs/architecture.md`](docs/architecture.md).
Package-by-package ownership: [`docs/backend-code-structure.md`](docs/backend-code-structure.md).

## Testing

The local gate is the backend Go build and race-enabled test suite:

```bash
cd backend && go build ./... && go test -race ./...
```

GitHub Actions is the authoritative pre-merge gate; mirror its commands here
when in doubt. See [`AGENTS.md`](AGENTS.md) for the regen workflow when
touching the daemon API surface (`npm run sqlc`, `npm run api`).

## Status and roadmap

Progress tracking lives in [`docs/STATUS.md`](docs/STATUS.md): what is shipped
on `main` today, what is still in flight, and the linked
[`rewrite`](https://github.com/aoagents/agent-orchestrator/milestone/1)
milestone on GitHub.

## Contributing

Repo layout and the worker contract live in [`AGENTS.md`](AGENTS.md). Keep
changes surgical, follow the package boundaries documented in
[`docs/backend-code-structure.md`](docs/backend-code-structure.md), and prefer
adding daemon HTTP routes over leaking storage / runtime into the CLI.
