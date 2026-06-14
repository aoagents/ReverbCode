# Backend Code Structure

This document describes package ownership for the Go backend. It is about where
code belongs. See [architecture.md](architecture.md) for lifecycle behavior,
status derivation, persistence, CDC, and invariants.

## Goal

The backend is a local daemon that supervises coding-agent sessions. The code
needs clear homes for product workflows, protocol surfaces, persistence, and
replaceable external systems without turning any single package into a catch-all.

The current structure is a layered hybrid:

- `domain` holds shared product vocabulary and durable fact records.
- `service/*` owns controller-facing product use cases and read models.
- `session_manager` owns internal session mutations and resource orchestration.
- `lifecycle` owns the durable session fact reducer.
- `ports` defines narrow capability interfaces consumed by core code.
- `adapters/*` implements those capabilities with real external systems.
- `storage/sqlite` and `cdc` own persistence and change delivery.
- `httpd` and `cli` own protocol concerns.
- `daemon` wires the production graph together.

## Package Roles

### `internal/domain`

`domain` is AO's shared product language. Keep it stable and free of
infrastructure imports.

Belongs here:

- shared IDs such as `ProjectID`, `SessionID`, and `IssueID`;
- shared enums and status vocabulary;
- durable fact records that multiple packages must agree on;
- PR, tracker, project, and session vocabulary that is not transport-specific.

Does not belong here:

- HTTP request/response DTOs;
- CLI output shapes;
- OpenAPI wrapper/envelope types;
- sqlc generated rows;
- GitHub, Zellij, Claude, Codex, or OpenCode payloads;
- one-resource controller helper types.

Rule of thumb: if AO would still use the concept after replacing HTTP, the CLI,
SQLite, GitHub, Zellij, and every agent adapter, and more than one package needs
the exact vocabulary, it may belong in `domain`.

### `internal/service/*`

`service` packages are the controller-facing application boundary.

Current examples:

```txt
internal/service/project
internal/service/session
internal/service/pr
internal/service/review
```

Belongs here:

- resource use cases called by HTTP controllers and CLI-backed API flows;
- resource read models and command/result types;
- display-model assembly, such as session status derived from session and PR
  facts;
- resource-specific validation and user-facing errors;
- small store interfaces consumed by the service.

Does not belong here:

- low-level runtime/workspace/agent process control;
- raw sqlc generated rows as public service results;
- HTTP routing, path parsing, status-code decisions, or OpenAPI generation;
- concrete external adapter details.

For example, project API concepts live in `internal/service/project`, not in
`domain` and not in a top-level `internal/project` package.

### `internal/session_manager`

`session_manager` owns internal session commands: spawn, restore, kill, cleanup,
and send-related orchestration over runtime, workspace, agent, storage,
messenger, and lifecycle dependencies.

Belongs here:

- multi-step session mutations;
- rollback/cleanup sequencing when spawn partially succeeds;
- resource teardown safety;
- internal errors such as not found, terminated, or not restorable.

Does not belong here:

- HTTP request decoding;
- CLI formatting;
- controller-facing list/get read-model assembly;
- terminal WebSocket framing.

The split is intentional: `service/session` is the product/API boundary;
`session_manager` is the internal command engine.

### `internal/lifecycle`

`lifecycle` is the canonical write path for durable session lifecycle facts. It
reduces runtime observations, activity signals, spawn completion, termination,
and PR observations into small persisted facts.

Belongs here:

- updates to lifecycle-owned session facts;
- guardrails around runtime/activity observations;
- lifecycle-triggered agent nudges for actionable PR facts.

Does not belong here:

- display status persistence;
- HTTP/CLI DTOs;
- direct adapter implementation details;
- PR row persistence.

The UI status is derived at read time by service code. Do not store display
status in lifecycle or SQLite.

### `internal/ports`

`ports` contains narrow capability interfaces and shared adapter-facing structs.
It connects core code to replaceable systems.

Current capability examples:

- `Runtime`
- `Workspace`
- `Agent`
- `AgentResolver`
- `AgentMessenger`
- `PRWriter`

Belongs here:

- interfaces consumed by core packages and implemented by adapters;
- capability structs such as `RuntimeConfig`, `WorkspaceConfig`, and
  `SpawnConfig`;
- vocabulary needed at the boundary between core orchestration and adapters.

Does not belong here:

- resource read models like project/session API responses;
- HTTP request/response DTOs;
- sqlc rows;
- concrete adapter options;
- one-off interfaces that only a single package needs internally.

Keep `ports` capability-oriented. It should not become the dumping ground for
every manager, DTO, and resource contract.

### `internal/adapters/*`

Adapters are concrete implementations of external systems.

Current examples:

```txt
internal/adapters/agent/claudecode
internal/adapters/agent/codex
internal/adapters/agent/opencode
internal/adapters/runtime/zellij
internal/adapters/workspace/gitworktree
internal/adapters/scm/github
internal/adapters/tracker/github
```

Adapters should be leaves in the import graph. They translate external behavior
into AO ports and domain concepts; they should not own product workflows.

Good:

```txt
session_manager -> ports.Runtime
adapters/runtime/zellij -> ports + domain
adapters/workspace/gitworktree -> ports + domain
daemon -> adapters + services + storage
```

Avoid:

```txt
domain -> adapters
service/session -> adapters/runtime/zellij
httpd/controllers -> storage/sqlite/store
adapters/* -> httpd
```

### `internal/storage/sqlite`

`storage/sqlite` owns SQLite setup, migrations, sqlc generated code, and store
implementations.

Belongs here:

- connection setup and PRAGMAs;
- goose migrations;
- sqlc queries and generated code;
- table-specific store methods;
- transactions and CDC-triggered persistence behavior.

Does not belong here:

- HTTP response types;
- CLI output formatting;
- product display status rules;
- external adapter logic.

Generated sqlc types should stay behind store methods. Services and lifecycle
code should work with domain records or service read models, not generated rows.

### `internal/cdc`

`cdc` owns `change_log` polling and event broadcasting. SQLite triggers append
durable events to `change_log`; the poller tails that table and fans events out
to subscribers.

Belongs here:

- event type definitions for the CDC stream;
- poller and broadcaster logic;
- subscriber fan-out behavior.

Does not belong here:

- terminal byte streams;
- product workflow decisions;
- database schema ownership.

### `internal/terminal`

`terminal` owns the terminal session protocol and PTY attach management used by
the HTTP mux. Every client that opens a pane gets its own `zellij attach` PTY —
zellij owns screen state and scrollback and replays its init handshake + full
repaint per attach, so there is no shared per-pane buffer.

Belongs here:

- per-client attachment lifecycle (liveness gating, re-attach backoff);
- input/output framing independent of HTTP;
- PTY-backed attach handling and terminal protocol tests.

`httpd` adapts WebSocket connections to terminal interfaces; `terminal` should
not import `httpd`.

### `internal/httpd`

`httpd` is the HTTP protocol adapter.

Belongs here:

- routing and middleware;
- HTTP request decoding and response encoding;
- path/query parameter handling;
- status-code mapping;
- API error envelopes;
- OpenAPI generation and serving;
- WebSocket upgrade handling for terminal mux.

Controllers call service managers and translate service results/errors into HTTP
responses. Controllers should not reach directly into concrete adapters or the
SQLite store.

HTTP-only request/response wrappers belong in `httpd` or
`httpd/controllers`. Application read models shared by controller and CLI flows
belong in the owning `service/*` package.

### `internal/cli`

`cli` owns the user-facing `ao` command. It should stay thin:

- discover the local daemon;
- call the daemon's loopback HTTP API;
- format command output;
- start/stop/status/doctor process control.

The CLI should not duplicate daemon business logic. If a command needs product
behavior, put the behavior in the daemon service/API path and have the CLI call
that path.

### `internal/daemon`

`daemon` is the production composition root. It wires config, logging, SQLite,
CDC, lifecycle, reaper, runtime, terminal manager, services, HTTP, and shutdown.

Belongs here:

- production dependency construction;
- adapter registration;
- startup/shutdown sequencing;
- cross-component wiring.

Does not belong here:

- business logic that should be testable in service, lifecycle, or manager
  packages;
- adapter implementation details.

## Interface Placement

Prefer interfaces near their consumers, except for shared capabilities.

- If only one package consumes an abstraction, define the smallest interface in
  that package.
- If multiple core packages consume a replaceable capability, define it in
  `ports`.
- If HTTP controllers need a resource service, use the owning `service/*`
  manager interface.
- Return concrete types from constructors unless callers genuinely need an
  interface.

## Current Tree

The current main-line shape is:

```txt
backend/
  cmd/ao/                       # CLI entrypoint
  main.go                       # daemon entrypoint compatibility
  sqlc.yaml

  internal/domain/              # shared product vocabulary and durable facts
  internal/ports/               # capability interfaces
  internal/service/
    project/                    # project API/use-case boundary
    session/                    # session API/use-case boundary
    pr/                         # PR observation/action service
    review/                     # code-review API/use-case boundary
  internal/session_manager/     # internal session command engine
  internal/lifecycle/           # durable lifecycle fact reducer
  internal/observe/scm/         # SCM (GitHub) observer loop
  internal/observe/reaper/      # runtime liveness observation loop
  internal/storage/sqlite/      # DB, migrations, queries, generated sqlc, stores
  internal/cdc/                 # change_log poller and broadcaster
  internal/terminal/            # terminal session protocol and PTY handling
  internal/httpd/               # HTTP API, controllers, OpenAPI, terminal mux
  internal/cli/                 # user-facing ao command
  internal/daemon/              # production wiring and shutdown
  internal/config/              # daemon env/default config
  internal/adapters/            # concrete agent/runtime/workspace/SCM/tracker adapters
```

## Adding New Code

Use these defaults:

- New HTTP route: add controller/API code in `httpd`, call a `service/*`
  package, and update OpenAPI generation/spec tests.
- New product resource: put shared IDs/vocabulary in `domain`, use cases and
  read models in `service/<resource>`, storage in `storage/sqlite`, and external
  system seams in `ports`.
- New adapter: implement a `ports` interface under `adapters/<capability>/<impl>`
  and wire it in `daemon`.
- New persisted fact: add a migration, sqlc query, store method, domain record or
  event vocabulary, and CDC behavior when the UI/API must observe it.
- New CLI command: keep command parsing/formatting in `cli`; call the daemon API
  rather than reimplementing daemon behavior.

## Project Routes Example

Project-owned concepts live in `internal/service/project`:

- project read models;
- project add/remove command types;
- project validation and user-facing errors;
- the `Manager` contract consumed by HTTP controllers.

`internal/httpd/controllers` remains responsible for:

- route registration;
- JSON decoding/encoding;
- HTTP status codes and error envelopes;
- mapping service errors to responses.

When a type is ambiguous, ask whether it is a product use-case/read model or an
HTTP wire wrapper. Product use-case/read models belong in `service/project`;
HTTP wire wrappers belong in `httpd`.
