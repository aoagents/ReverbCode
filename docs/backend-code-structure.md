# Backend code structure

This document describes the target package structure for the Go backend and the
rules for deciding where new code belongs. It is intentionally about package
ownership and maintainability; see `architecture.md` for lifecycle behavior and
state-machine invariants.

## Goal

The backend coordinates long-running AI coding sessions. It needs to keep the
lifecycle core strict while still giving product resources, adapters, and API
surfaces obvious homes.

The target structure is a hybrid:

- a small shared `domain` package for stable product vocabulary,
- feature-owned application packages for product workflows,
- focused capability packages for replaceable external systems,
- protocol packages for HTTP/CLI transport concerns.

This avoids two failure modes:

- a central `ports` package becoming a catch-all for unrelated DTOs,
  interfaces, and adapter contracts;
- a pure feature-folder layout that weakens the lifecycle/session invariants by
  scattering shared state vocabulary.

## Package roles

### `internal/domain`

`domain` is AO's shared product language. It should stay small, stable, and
free of infrastructure imports.

Belongs here:

- shared IDs: `ProjectID`, `SessionID`, `IssueID`, tracker IDs;
- canonical lifecycle/session state;
- pure derived status logic;
- normalized cross-provider vocabulary that many packages must agree on.

Does not belong here:

- HTTP request/response DTOs;
- OpenAPI wrapper shapes;
- SQLite rows or sqlc generated types;
- GitHub/tmux/zellij-specific payloads;
- feature-specific config patches and repair results.

Rule of thumb: if AO would still use the concept after replacing HTTP, GitHub,
tmux/zellij, and SQLite, and more than one feature needs the exact vocabulary,
it may belong in `domain`.

### Feature packages

Feature packages own product workflows and feature-specific types.

Examples:

```txt
internal/project
internal/session
internal/lifecycle
internal/scm
```

A feature package may contain:

- feature entities and read models;
- application command/result types;
- the feature manager/service implementation;
- feature-specific validation and errors;
- small interfaces the feature consumes.

Example: `ProjectID` is shared vocabulary and belongs in `domain`; `Project`,
`Summary`, `Degraded`, `TrackerConfig`, `SCMConfig`, and `ReactionConfig` are
project-owned concepts and belong in `internal/project`.

### Capability packages and adapters

A capability is something AO needs from an external system. An adapter is a
concrete implementation.

Recommended long-term shape:

```txt
internal/runtime/
  runtime.go
  tmux/
  zellij/

internal/workspace/
  workspace.go
  gitworktree/

internal/storage/
  store.go
  sqlite/

internal/tracker/
  tracker.go
  github/

internal/notify/
  notifier.go
  desktop/
  slack/

internal/agent/
  agent.go
  codex/
  claude/
  opencode/
```

The current tree still uses `internal/ports` for several of these seams. That is
acceptable during integration, but new code should avoid adding unrelated
resource DTOs or single-resource inbound interfaces to `ports`.

Adapters should be leaves in the import graph. They translate external details
into AO concepts; they should not own product workflows.

Good:

```txt
session -> runtime
runtime/tmux -> runtime + domain
workspace/gitworktree -> workspace + domain
storage/sqlite -> storage + domain
```

Avoid:

```txt
domain -> httpd
domain -> adapters/tracker/github
session -> adapters/runtime/tmux
httpd -> storage/sqlite
```

### `internal/httpd`

`httpd` is the HTTP protocol adapter. It owns:

- routing and middleware;
- request decoding;
- path/query parameter handling;
- HTTP status codes;
- JSON response encoding;
- API error envelopes;
- OpenAPI serving and validation.

It should call feature managers and translate their results/errors into HTTP
responses. It should not reach directly into runtime, workspace, storage, or
provider adapters.

HTTP-only request/response wrappers belong in `httpd`. Application commands
shared by HTTP and CLI can live in the owning feature package.

## Interface placement

Prefer interfaces near their consumers.

- If only one package consumes an abstraction, define the interface there.
- If HTTP and CLI both need the same application workflow, define the manager in
  the feature package, e.g. `project.Manager`.
- If many implementations satisfy the same external capability, define that
  interface in a focused capability package, e.g. `runtime.Runtime`.
- Avoid adding new resource-specific inbound managers to central `ports`.

Return concrete types from constructors unless callers genuinely need an
interface.

## Target tree

This is the direction to migrate toward:

```txt
backend/
  main.go                         # composition root for now

  internal/domain/
    lifecycle.go
    session.go
    status.go
    tracker.go
    decide/

  internal/lifecycle/
    manager.go
    decide_bridge.go
    reactions.go

  internal/session/
    manager.go
    types.go                      # eventually owns Spawn/Kill/Cleanup inputs

  internal/project/
    manager.go
    types.go
    dto.go

  internal/httpd/
    router.go
    server.go
    errors.go
    json.go
    apispec/
    controllers/                  # or resource route files while small

  internal/observe/
    reaper/
    scm/

  internal/runtime/
    runtime.go
    tmux/
    zellij/

  internal/workspace/
    workspace.go
    gitworktree/

  internal/storage/
    store.go
    sqlite/
      migrations/
      queries/
      sqlc/

  internal/tracker/
    tracker.go
    github/

  internal/notify/
    notifier.go
```

## Migration guidance

Do not perform a large package reshuffle for its own sake. Use this structure as
the target when touching code for real feature work.

Recommended sequence:

1. Keep `domain` small. Move only shared vocabulary there.
2. Let new resources create feature packages (`project`, later `scm`, etc.).
3. Stop adding unrelated DTOs and one-off managers to `ports`.
4. Split `ports` gradually into focused capability packages as implementations
   land or need multiple adapters.
5. Keep HTTP transport DTOs in `httpd` unless they are intentionally shared
   application command/result types.
6. Preserve the LCM writer contract: observers, HTTP, CLI, and session workflows
   report facts/commands to the LCM; they do not write canonical lifecycle state
   directly.

## Applying this to project routes

`internal/project` is the right home for project-owned concepts:

- project entities/read models;
- project-specific behavior config;
- project add/update/remove/repair/reload command/result types;
- a `Manager` contract if both HTTP and CLI will call it.

`internal/httpd` remains responsible for:

- route registration;
- OpenAPI;
- JSON request/response wrappers when they differ from application types;
- mapping project errors to HTTP status codes and API error envelopes.

When a type is ambiguous, ask whether it is a product command or an HTTP wire
shape. Product commands belong in `project`; HTTP wire shapes belong in `httpd`.
