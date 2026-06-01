# AO CLI Foundation

This page is the running decision log for the Agent Orchestrator CLI. Keep new
CLI decisions here as the command surface grows.

## Current State

This branch implements the daemon-control foundation. AO now has a Go/Cobra
`ao` binary that can start, inspect, diagnose, and stop the local backend daemon
end to end.

What works now:

- `ao start` starts the daemon in the background and waits for `/readyz`.
- `ao status` and `ao status --json` report stopped, stale, unhealthy,
  not-ready, or ready daemon state.
- `ao stop` gracefully stops the daemon via the loopback `POST /shutdown`
  endpoint, only after verifying the daemon's identity from `running.json`.
- `ao daemon` is the hidden internal daemon entrypoint used by `ao start`.
- `ao doctor` (and `ao doctor --json`) checks config, data dir, the database
  file's presence, daemon state, and local tool availability for `git` and `zellij`. It never opens or migrates the store — the daemon is the sole
  writer/migrator, so doctor only reports whether the database exists yet.
- `ao completion` generates shell completions for `bash`, `zsh`, `fish`, and
  `powershell`.
- `ao version` and `ao --version` print build metadata.
- `go run .` still works as a compatibility wrapper around `internal/daemon.Run`.

Manual smoke test:

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
```

What is intentionally not implemented yet:

- `ao project ...`
- `ao spawn`
- `ao session ...`
- `ao send`
- `ao events ...`

Next steps:

1. Wire the existing project manager/controller shell into the daemon with a
   durable SQLite-backed project store.
2. Implement `ao project list/add/show/remove` against `/api/v1/projects`.
3. Wire production Session Manager dependencies: project-backed repo resolver,
   Zellij runtime, first agent adapter, and AgentMessenger.
4. Add `/api/v1/sessions`, then implement `ao spawn`, `ao session ...`, and
   `ao send`.
5. Add `/events` SSE and durable event-list reads, then implement
   `ao events tail/list`.

## Decision

AO will use a single Go CLI binary built with
[Cobra](https://github.com/spf13/cobra).

The CLI is a thin client for the Go daemon. It should not call SQLite, runtime
adapters, agent adapters, workspace adapters, or SCM integrations directly. It
should start, discover, inspect, and command the daemon through the loopback API
and the existing `running.json` handshake.

Initial rules:

- The binary name is `ao`.
- `ao daemon` is the hidden/internal entrypoint for the long-running daemon.
- User-facing commands call the daemon over loopback after reading
  `running.json`.
- Commands that mutate core AO state go through HTTP API routes, not direct
  stores.
- Commands support predictable text output first and `--json` where automation
  is likely.
- Do not introduce Viper in the foundation. Start with explicit flags and a
  small config/client layer, then add config loading once the shape is real.

## References

These projects inform the direction, but AO should keep its own command surface
smaller at first.

| Project | CLI stack | What to take |
|---|---|---|
| [Gastown](https://github.com/gastownhall/gastown) | Go + Cobra, with Charmbracelet packages for richer terminal UI | Simple `cmd/<binary>/main.go` delegating to internal command construction. Useful confirmation that Cobra is the right default for this size of Go CLI. |
| [GitHub CLI](https://github.com/cli/cli) | Go + Cobra | Command factories, explicit IO streams, JSON output, and testable command construction. |
| [Docker CLI](https://github.com/docker/cli) | Go + Cobra | Daemon/client split, command groups, signal handling, and plugin-aware CLI layout. |
| [kubectl](https://github.com/kubernetes/kubectl) | Go + Cobra | Large command tree patterns and IO abstractions. It is a useful ceiling, not a shape to copy now. |
| [Tailscale CLI](https://github.com/tailscale/tailscale) | Go + ffcli | Useful daemon-backed product model: a CLI talks to a local daemon. Do not copy the framework choice. |

The old AO TypeScript CLI is a product/workflow reference only. We should not
port its implementation because it mixes CLI, storage, runtime, and project
logic in-process. The rewrite needs the CLI to sit outside the core daemon.

## Current Legacy CLI Inventory

Inventory source: installed `ao` binary at version `0.9.2`, plus the old
`packages/cli/src/program.ts` and `packages/cli/src/commands/*.ts` files.

Count:

- 25 public top-level commands, excluding Commander-generated `help`.
- 26 visible top-level commands if generated `help` is counted.
- 64 explicit public command nodes when nested subcommands are counted.
- 1 hidden internal command: `completion __complete`.
- No aliases are registered in the old Commander source.

Top-level commands:

| Command | Legacy purpose | Foundation decision |
|---|---|---|
| `start` | Start orchestrator agent and dashboard | Keep, but redefine as daemon start. |
| `stop` | Stop orchestrator agent and dashboard | Keep, daemon stop. |
| `status` | Show all sessions and project/session health | Keep, daemon and session status. |
| `spawn` | Spawn a single agent session | Keep after session API exists. |
| `batch-spawn` | Spawn many sessions | Defer. |
| `session` | Manage sessions | Keep a smaller subset after session API exists. |
| `send` | Send a message to a session | Keep after messaging API exists. |
| `acknowledge` | Agent self-reporting hook | Defer or replace with internal API. |
| `report` | Agent workflow transition hook | Defer or replace with internal API. |
| `review-check` | Trigger agents from review comments | Defer. |
| `review` | Manage AO-local reviewer runs | Defer. |
| `dashboard` | Start web dashboard | Defer to Electron/frontend lane. |
| `open` | Open terminal/dashboard | Defer. |
| `verify` | Verify issue after staging check | Defer. |
| `doctor` | Run install/env/runtime checks | Keep. |
| `update` | Upgrade AO | Defer to packaging/release lane. |
| `setup` | Configure integrations | Defer. |
| `plugin` | Plugin marketplace/install flow | Defer. |
| `notify` | Notification test commands | Drop until notification capabilities are redesigned. |
| `project` | Manage registered projects | Keep after project API exists. |
| `migrate-storage` | Legacy storage migration | Drop for rewrite unless a real migration appears. |
| `completion` | Generate shell completions | Keep. |
| `events` | Query activity event log | Keep a small `tail`/`list` surface after event API exists. |
| `config` | Read/write old global config | Defer. Avoid until config shape is stable. |
| `config-help` | Print old config schema | Drop. |

Nested legacy commands:

| Parent | Subcommands |
|---|---|
| `session` | `ls`, `attach`, `kill`, `cleanup`, `claim-pr`, `restore`, `remap` |
| `review` | `run`, `execute`, `send`, `list` |
| `setup` | `dashboard`, `desktop`, `webhook`, `slack`, `discord`, `composio`, `composio-slack`, `composio-discord`, `composio-discord-bot`, `composio-mail`, `openclaw` |
| `plugin` | `list`, `search`, `create`, `install`, `update`, `uninstall` |
| `project` | `ls`, `add`, `rm`, `set-default` |
| `events` | `list`, `search`, `stats` |
| `config` | `set`, `get` |
| `notify` | `test` |
| `completion` | `zsh`, hidden `__complete` |

## Initial Command Surface

The first CLI should make AO installable, startable, inspectable, and stoppable
before trying to recreate the old product surface.

### Foundation Commands

These are the first commands to implement.

| Command | Purpose | Notes |
|---|---|---|
| `ao start` | Start the daemon, wait for `/readyz`, and print PID/port. | Reads the same config env as the daemon. Should be idempotent when an existing healthy daemon is already running. |
| `ao stop` | Stop the running daemon. | Reads `running.json`, sends graceful termination, waits for run-file removal, and reports stale/dead daemon state clearly. |
| `ao status` | Show daemon status and, once APIs exist, project/session summary. | First version can show run-file, process liveness, `/healthz`, `/readyz`, uptime, and port. Add `--json`; add `--watch` once useful. |
| `ao daemon` | Hidden internal daemon entrypoint. | This replaces the current direct `go run .` daemon entrypoint once `main.go` is extracted into `internal/daemon`. |
| `ao doctor` | Diagnose the local environment. | Start with daemon/run-file/port checks, required binaries, config dir/data dir permissions, and runtime availability. |
| `ao completion` | Generate shell completions. | Cobra can support `bash`, `zsh`, `fish`, and `powershell`. |
| `ao version` | Print CLI and build metadata. | Implement as both `ao version` and Cobra's `--version` flag. |

This gives a useful first release even before project/session mutation routes are
complete.

### First Core Application Commands

These are the next commands once daemon HTTP routes expose the needed managers.

| Command | Purpose | Depends on |
|---|---|---|
| `ao project list` | List registered projects. | Project API. Alias `ls` is acceptable for old muscle memory. |
| `ao project add <path-or-url>` | Register a project. | Project API and project identity rules. |
| `ao project show <id>` | Inspect project config and health. | Project API. |
| `ao project remove <id>` | Archive/remove a project. | Project API. Alias `rm` is acceptable. |
| `ao spawn [issue]` | Spawn one coding-agent session. | Session Manager HTTP route, tracker lookup, workspace/runtime/agent adapters. |
| `ao session list` | List sessions across projects or one project. | Session API. Alias `ls` is acceptable. |
| `ao session show <session>` | Show one session with lifecycle, PR, CI, runtime, and paths. | Session API. |
| `ao session attach <session>` | Attach to the runtime terminal. | Runtime API or direct terminal attach contract exposed by daemon. |
| `ao session kill <session>` | Kill a session and clean up safely. | Session Manager `Kill`. |
| `ao session restore <session>` | Restore a terminated/crashed session. | Session Manager `Restore`. |
| `ao send <session> [message...]` | Send instructions to a running session. | AgentMessenger route. |
| `ao events tail` | Follow daemon activity events. | SSE/CDC API. |
| `ao events list` | List recent activity events. | Event read API. |

This is the smallest surface that covers the core product loop:

1. Register a repo.
2. Start AO.
3. Spawn work.
4. Inspect work.
5. Intervene in work.
6. Stop AO.

## Explicit Deferrals

Do not include these in the CLI foundation:

- `batch-spawn`: valuable, but it multiplies error handling before single-spawn
  semantics are stable.
- `dashboard` and `open`: frontend/Electron should own the primary dashboard
  launch path first.
- `review`, `review-check`, and `verify`: useful workflow automation, but not
  required to run core AO.
- `setup`, `plugin`, and `notify`: integration/plugin surface should come after
  the daemon API and config model settle.
- `update`: belongs with distribution and release packaging.
- `config` and `config-help`: wait for a stable Go config model. Avoid copying
  the old TypeScript global config behavior.
- `migrate-storage`: old storage migration is not part of the rewrite unless a
  concrete migration requirement appears.
- `acknowledge` and `report`: these are agent self-reporting hooks. Prefer a
  daemon/internal protocol before exposing them as durable user CLI commands.

## Implementation Plan

1. Add Cobra to `backend/go.mod`.
2. Move current daemon startup from `backend/main.go` into
   `backend/internal/daemon.Run(ctx, opts)`.
3. Add `backend/cmd/ao/main.go` as the only user binary entrypoint.
4. Add `backend/internal/cli` for command construction, IO streams, process
   launching, run-file discovery, loopback HTTP client, and output formatting.
5. Implement `ao daemon` first so the current daemon behavior is preserved.
6. Implement `ao start`, `ao stop`, and `ao status` around `running.json` and
   `/healthz`/`/readyz`.
7. Add `ao doctor`, `ao completion`, and `ao version`.
8. Add command tests using Cobra command construction with fake IO, fake process
   runner, and fake daemon client. Keep daemon integration tests in the daemon
   packages.

Suggested package layout:

```text
backend/
  cmd/
    ao/
      main.go
  internal/
    cli/
      root.go
      start.go
      stop.go
      status.go
      doctor.go
      completion.go
      version.go
      client.go
      output.go
      process.go
    daemon/
      daemon.go
```

Acceptance criteria for the foundation:

- `go run ./cmd/ao daemon` behaves like today's `go run .`.
- `go run ./cmd/ao start` starts the daemon and waits until `/readyz` returns
  ready.
- `go run ./cmd/ao status --json` works when the daemon is running, stopped, and
  stale.
- `go run ./cmd/ao stop` gracefully stops the daemon and removes `running.json`.
- `go test ./...`, `go vet ./...`, and `go test -race ./...` pass.

## Implementation Readiness

This section records what the CLI can connect to in the current codebase and
what still needs to be built. Inventory date: 2026-05-31 after merging
`origin/main` at `438b830`.

### Implemented Foundation

The daemon-control foundation now exists in `backend/cmd/ao` and
`backend/internal/cli`.

Implemented commands:

- `ao daemon` hidden/internal daemon entrypoint.
- `ao start` starts the daemon, waits for `/readyz`, and supports `--json`,
  `--timeout`, and `--log-file`.
- `ao stop` stops the daemon from `running.json`, removes stale run-files, and
  supports `--json` and `--timeout`.
- `ao status` reports stopped/stale/unhealthy/not-ready/ready states and
  supports `--json`.
- `ao doctor` checks config, data dir, database-file presence, daemon state, and
  local tool availability for `git`, `zellij`; supports `--json`. It
  does not open or migrate the store (the daemon owns that).
- `ao completion` generates `bash`, `zsh`, `fish`, and `powershell`
  completions.
- `ao version` prints build metadata.

The old `backend/main.go` remains as a compatibility wrapper around
`internal/daemon.Run`, so `go run .` still starts the daemon while scripts move
to `go run ./cmd/ao ...`.

### Already Implemented and Directly Usable by the CLI

These pieces are available now and are enough to build the daemon-management
part of the CLI.

| Area | Existing code | CLI use |
|---|---|---|
| Daemon config | `backend/internal/config` loads `AO_PORT`, `AO_REQUEST_TIMEOUT`, `AO_SHUTDOWN_TIMEOUT`, `AO_RUN_FILE`, and `AO_DATA_DIR`. Host is fixed to `127.0.0.1`. | `ao start`, `ao daemon`, `ao status`, and `ao doctor` can share the same config resolution. |
| HTTP server lifecycle | `backend/internal/httpd.Server` binds loopback, writes `running.json`, serves until context cancellation, then removes `running.json`. | `ao daemon` can preserve today's daemon behavior after extraction into `internal/daemon`. |
| Health probes | `GET /healthz` and `GET /readyz`. | `ao start` can wait for readiness; `ao status` and `ao doctor` can check daemon health. |
| Run-file handshake | `backend/internal/runfile` reads, writes, removes, and stale-checks `running.json`. | `ao status` can discover PID/port; `ao stop` can find the process; `ao start` can detect an already-running daemon. |
| Durable store | `backend/internal/storage/sqlite` opens SQLite, runs goose migrations, uses WAL, stores projects/sessions/PR/check/comment rows, and reads `change_log`. | Not directly called by user CLI commands, but confirms the daemon has a durable backend once APIs expose it. |
| CDC substrate | `backend/internal/cdc` poller and broadcaster exist; daemon starts the poller with `startCDC`. | Future `ao events tail` can build on this once an SSE/API transport exists. |
| Lifecycle manager | `backend/internal/lifecycle` is implemented and currently wired in daemon startup. | Session/status APIs can use it; CLI must wait for HTTP routes rather than calling it directly. |
| Reaper timer | `backend/internal/observe/reaper` exists and is wired. | Runtime liveness will be available once the configured Zellij runtime is wired. |

### Implemented Internally but Not Reachable by CLI Yet

These are real backend components, but the CLI cannot responsibly use them until
they are wired into the daemon and exposed through HTTP.

| Area | Existing code | Missing before CLI can use it |
|---|---|---|
| Project API pieces | `internal/project` has manager/controller DTOs, `/api/v1/projects` routes exist, and `sqlite.Store` has project CRUD. | Durable project-store adapter/wiring in the daemon and CLI commands. The daemon currently constructs the router with nil API deps, so project routes are not product-usable from `ao` yet. |
| Session Manager | `backend/internal/session.Manager` implements `Spawn`, `Kill`, `Restore`, `List`, `Get`, `Send`, and `Cleanup`. | Production daemon wiring with real runtime, agent, workspace, messenger, and HTTP routes. |
| Runtime adapters | Zellij adapter implements `ports.Runtime` and also have attach/send/output helpers. | Daemon wiring, attach/send abstractions in ports/API, and runtime preflight checks. |
| Workspace adapter | git worktree adapter implements create/destroy/restore/list with safety checks. | Repo resolver backed by registered projects and daemon wiring into Session Manager. |
| GitHub issue tracker | `backend/internal/adapters/tracker/github` implements read-only issue `Get`, `List`, and `Preflight`. | Tracker registry/config, spawn prompt hydration, and project tracker metadata. |
| PR facts storage | SQLite PR/check/comment writes and CDC triggers exist. | SCM/PR observer that fetches GitHub PR/CI/review facts and calls the PR service. |
| Session read model | `SessionManager.List/Get` derive display status from `activity_state`, `is_terminated`, and PR facts. | HTTP response DTOs and API routes for CLI/frontend reads. |

### Still Missing

These are the main gaps before the full initial command set is real.

| Gap | Blocks |
|---|---|
| Product API client package with run-file discovery. | `project`, `spawn`, `session`, `send`, `events list`, richer `status`. |
| Shutdown mechanism choice: PID signal now, optional `POST /api/v1/daemon/shutdown` later. | `ao stop` polish and cross-platform behavior. |
| Session/send API route surface under `/api/v1`. | `spawn`, `session`, `send`, richer `status`. |
| Project API daemon wiring. | `ao project list/add/show/remove`. |
| SSE route for live CDC events plus durable catch-up reads. | `ao events tail`, frontend live updates. |
| Agent adapters for supported harnesses (`codex`, `claude-code`, etc.). | `ao spawn`, `ao session restore`. |
| AgentMessenger implementation over Zellij. | `ao send` and lifecycle PR nudges. |
| Zellij runtime wired into the daemon. | Reaper liveness, `session attach`, spawn/kill/restore runtime work. |
| Activity hooks or agent self-report protocol. | Accurate working/idle/needs-input status beyond runtime/PR facts. |
| Project/tracker config model. | `project add/show`, tracker-backed `spawn`, `doctor` config checks. |
| OpenAPI/DTO/error contract. | Stable CLI/frontend API clients and tests. |

### Command Readiness Matrix

| Command | Can implement now? | Existing support | Remaining work |
|---|---:|---|---|
| `ao daemon` | Implemented | Current daemon startup is extracted to `internal/daemon.Run`. | None for foundation. |
| `ao start` | Implemented | Config, run-file stale check, HTTP readiness probes. | Later: package-manager/service integration if needed. |
| `ao stop` | Implemented | Run-file discovery gives PID/port; server exits cleanly on SIGINT/SIGTERM. | Optional later shutdown HTTP route. |
| `ao status` | Partially implemented | Run-file, process liveness via PID, `/healthz`, `/readyz`. | Rich project/session summary waits for `/api/v1/projects` and `/api/v1/sessions`. |
| `ao doctor` | Partially implemented | Config resolution, run-file, database-file presence (no open/migrate), runtime binary checks. | Deeper adapter preflights need daemon wiring/config and should be queried from the daemon, not run in-process. |
| `ao completion` | Implemented | Cobra generators. | None for foundation. |
| `ao version` | Implemented | Build metadata can be injected with `-ldflags`. | Release tooling needs to set metadata. |
| `ao project list/add/show/remove` | Not yet | Project manager/controller route shell and SQLite project CRUD exist. | Durable project-store adapter, daemon API wiring, and CLI HTTP client. CLI must not write SQLite directly. |
| `ao spawn` | Not yet | Session Manager exists; runtime/workspace/tracker pieces partly exist. | Agent adapters, registry/config wiring, project lookup, tracker hydration, HTTP route. |
| `ao session list/show` | Not yet | Store and Session Manager read model exist. | HTTP routes and response DTOs. |
| `ao session attach` | Not yet | Zellij has attach command helpers. | Runtime attach port/API and terminal-launch policy. |
| `ao session kill/restore` | Not yet | Session Manager implements both. | Production wiring and HTTP routes. |
| `ao send` | Not yet | Session Manager has `Send`; Zellij has send helpers. | AgentMessenger implementation, port/API wiring, busy/idle delivery policy. |
| `ao events tail/list` | Not yet | Durable `change_log`, CDC poller, in-process broadcaster. | SSE route and durable event-list route. |

### Recommended Build Order

1. Build CLI foundation around the daemon only: `daemon`, `start`, `stop`,
   `status`, `doctor`, `completion`, `version`.
2. Wire the existing project manager/controller shell into the daemon with a
   durable SQLite-backed store, then implement `project list/add/show/remove`.
3. Wire production Session Manager dependencies: project-backed repo resolver,
   Zellij runtime, first agent adapter, and AgentMessenger.
4. Add `/api/v1/sessions` and implement `spawn`, `session list/show/kill/restore`,
   and `send`.
5. Add `/events` SSE plus event-list reads, then implement `events tail/list`.
