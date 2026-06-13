# Design: typed per-project configuration

Status: **partially implemented** — `ProjectConfig` is typed, validated,
persisted (one `projects.config` JSON column), and surfaced via
`ao project set-config` + `PUT /projects/{id}/config`. The struct deliberately
carries only fields with a live consumer: `defaultBranch`, `env`, `symlinks`,
`postCreate`, `agentConfig`, and the `worker`/`orchestrator` role overrides are
wired at spawn; `sessionPrefix` feeds the display prefix. Settings whose
consumers do not yet exist — per-project `tracker`/`scm` config and prompt
`rules` — are intentionally **not** modeled yet and land in focused follow-up
PRs alongside the code that reads them (see "Sequencing" below). Cross-agent
`agentConfig.model`/`permissions` support is tracked in #157.

## Goal

Every per-project setting the legacy `agent-orchestrator.yaml` carried under
`projects:` should live as **typed, validated state** in SQLite, reachable
through exactly two entry points:

1. **CLI** — `ao project ...` (thin client → daemon HTTP)
2. **UI** — the dashboard project settings form

There is no YAML loader in the Go rewrite, so this is not about parsing a file —
it is about giving each former YAML field a typed home, a validation owner, and a
CLI/API/UI surface. No setting should be a free-form `map[string]any`.

## Principle: typed over map

The legacy `agentConfig` was an open `map` (`.passthrough()`), which is why early
storage modeled it as `map[string]any`. That defers validation to spawn time and
forces the UI to render raw JSON. We instead model each setting as a **typed Go
struct** with a `Validate()` method, so:

- bad values are rejected when **set** (CLI/API), not silently dropped at spawn;
- the OpenAPI spec and frontend TS types are generated with real fields;
- the UI renders a typed form instead of a JSON textarea.

Adapter-specific keys, if ever needed, become typed fields owned by `domain`
rather than an escape-hatch map.

## Field catalog (legacy `projects.<id>`) and home

`name`, `repo`, and `path` are first-class columns on `projects`. Every other
shipped setting lives as a key inside the single `projects.config` JSON blob;
settings without a live consumer are not modeled yet (see "Sequencing").

| YAML field                        | Type                   | Home                                        | Status                                         |
| --------------------------------- | ---------------------- | ------------------------------------------- | ---------------------------------------------- |
| `name`                            | string                 | `projects.display_name` (column)            | done                                           |
| `repo`                            | string                 | `projects.repo_origin_url` (column)         | done                                           |
| `path`                            | string                 | `projects.path` (column)                    | done                                           |
| `defaultBranch`                   | string                 | `config.defaultBranch`                      | done                                           |
| `sessionPrefix`                   | string                 | `config.sessionPrefix`                      | done                                           |
| `agentConfig`                     | `{model, permissions}` | `config.agentConfig`                        | done                                           |
| `orchestrator`/`worker` overrides | `{agent, agentConfig}` | `config.orchestrator` / `config.worker`     | done                                           |
| `env`                             | `map[string]string`    | `config.env`                                | done                                           |
| `symlinks`                        | `[]string`             | `config.symlinks`                           | done                                           |
| `postCreate`                      | `[]string`             | `config.postCreate`                         | done                                           |
| `agentRules` / `agentRulesFile`   | string                 | future `config.agentRules*`                 | not modeled (partial `SpawnConfig.AgentRules`) |
| `orchestratorRules`               | string                 | future `config.orchestratorRules`           | not modeled                                    |
| `tracker`                         | `{plugin, …}`          | future `config.tracker` (adapter-validated) | not modeled                                    |
| `scm`                             | `{plugin, webhook{…}}` | future `config.scm` (adapter-validated)     | not modeled                                    |
| `opencodeIssueSessionStrategy`    | enum                   | future `config.*`                           | not modeled                                    |
| `reactions`                       | per-project overrides  | future (own slice)                          | not modeled                                    |

## Typed model

```go
// domain
type AgentConfig struct {            // implemented
    Model       string         `json:"model,omitempty"`
    Permissions PermissionMode `json:"permissions,omitempty"`
}
func (c AgentConfig) Validate() error { ... }

// implemented today — only fields with a live consumer are modeled
type ProjectConfig struct {
    DefaultBranch string
    SessionPrefix string
    AgentConfig   AgentConfig
    Worker        RoleOverride          // {Harness, AgentConfig}
    Orchestrator  RoleOverride
    Env           map[string]string
    Symlinks      []string
    PostCreate    []string
    // future slices add fields here as their consumers land:
    //   AgentRules / AgentRulesFile / OrchestratorRules (prompt rules)
    //   Tracker TrackerConfig   // adapter-validated
    //   SCM     SCMConfig       // adapter-validated
}
```

Each leaf type owns a `Validate()`. Plugin-shaped settings (`tracker`, `scm`)
delegate to the selected adapter, mirroring how `agentConfig` is consumed by the
agent adapter.

## Storage strategy

The whole `ProjectConfig` is persisted as **one nullable JSON blob** — the
`projects.config` `TEXT` column (migration `0008_add_project_config.sql`). The
store marshals `ProjectConfig` to JSON on write and unmarshals on read; an empty
config (`IsZero`) persists SQL `NULL`. There are no per-field columns and no
child tables for any config setting:

- A single column keeps the schema stable as new typed fields are added — a new
  setting is a struct field plus a JSON key, never a migration.
- Validation lives in the domain type (`ProjectConfig.Validate` and each leaf's
  `Validate`), not in column constraints, so bad values are refused at set time.
- `env` is a plain `map[string]string` key in the blob, not a `project_env`
  child table.

> The originally proposed split — scalars in typed columns, small blobs in
> per-field JSON columns, `env` in a `project_env` child table — was
> **superseded**. The migration comment records the decision: a single JSON
> column persists the "shape of the YAML config" rather than splitting config
> into many columns. If an individual field ever needs its own column (e.g. to
> index or query on it), that becomes a future, field-specific migration.

## Surface

A project's config is set as a whole object through a single route, not via
per-group endpoints:

- **API** — `PUT /api/v1/projects/{id}/config` with body `{ "config": { … } }`
  replaces the project's config. The config may also be supplied at registration
  via `POST /api/v1/projects`. The daemon validates the typed config and rejects
  unknown fields.
- **CLI** — `ao project set-config <id>` with typed flags:
  - `--default-branch`, `--session-prefix`
  - `--model`, `--permission` (the `agentConfig` fields)
  - `--worker-agent`, `--orchestrator-agent` (role harness overrides)
  - `--env KEY=VALUE` (repeatable), `--symlink` (repeatable),
    `--post-create` (repeatable)
  - `--config-json '{…}'` to pass the whole object, `--clear` to remove all
    config, `--json` to print the updated project

  `set-config` replaces the config; there are no per-field subcommands such as
  `ao project env set`. `ao project get <id>` prints the resolved config.

- **UI** — a generated typed form, driven by the OpenAPI schema for the config
  object.

## Sequencing (one slice per PR)

Shipped slices (all landed inside the single `projects.config` blob, so identity
scalars and workspace provisioning were not separate column/table migrations):

1. **agentConfig (typed)** — established the typed+validated+surfaced pattern end
   to end.
2. **Project identity scalars** — `defaultBranch`, `sessionPrefix` (stop
   hardcoding/deriving them).
3. **Workspace provisioning** — `env`, `symlinks`, `postCreate`.
4. **Role overrides** — `worker` / `orchestrator` `{agent, agentConfig}`.

Remaining (future) slices, each adding a typed field to `ProjectConfig` (plus
validation, CLI flags, and UI) as its consumer lands — no schema migration
required:

5. **Rules** — `agentRules`, `agentRulesFile`, `orchestratorRules` (consolidate
   the partial `SpawnConfig.AgentRules` path).
6. **Tracker / SCM per-project** — typed config with adapter-owned validation.
7. **Per-project reactions** — integrate with the reaction engine; may warrant
   its own slice/storage rather than the config blob.

Each slice follows the same shape: domain field + `Validate()` → JSON key in the
config blob → service set/get → the single config route → CLI flags → UI form →
tests.
