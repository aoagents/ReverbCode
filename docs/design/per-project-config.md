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

## Field catalog (legacy `projects.<id>`) and target home

| YAML field | Type | Storage today | Target |
|---|---|---|---|
| `name` | string | `projects.display_name` | done |
| `repo` | string | `projects.repo_origin_url` | done |
| `path` | string | `projects.path` | done |
| `defaultBranch` | string | hardcoded `"main"` | `projects.default_branch` |
| `sessionPrefix` | string | derived | `projects.session_prefix` |
| `agentConfig` | `{model, permissions}` | **`projects.agent_config` (typed)** | **done (this PR)** |
| `orchestrator`/`worker` overrides | `{agent, agentConfig}` | — | typed role-override columns/blob |
| `env` | `map[string]string` | — | `project_env` table (key/value rows) |
| `symlinks` | `[]string` | — | `projects.symlinks` (JSON) |
| `postCreate` | `[]string` | — | `projects.post_create` (JSON) |
| `agentRules` / `agentRulesFile` | string | partial (`SpawnConfig.AgentRules`) | `projects.agent_rules*` |
| `orchestratorRules` | string | — | `projects.orchestrator_rules` |
| `tracker` | `{plugin, …}` | DTO stub only | `projects.tracker` (typed blob) + adapter validation |
| `scm` | `{plugin, webhook{…}}` | DTO stub only | `projects.scm` (typed blob) + adapter validation |
| `opencodeIssueSessionStrategy` | enum | — | `projects.opencode_session_strategy` |
| `reactions` | per-project overrides | — | `project_reactions` (own slice) |

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

- **Scalar fields** (`default_branch`, `session_prefix`, `agent_rules`, enums) →
  their own typed columns on `projects`.
- **Small structured blobs** (`agent_config`, `tracker`, `scm`, `symlinks`,
  `post_create`) → nullable JSON columns, marshaled/unmarshaled in the store
  (the pattern this PR established for `agent_config`).
- **Unbounded key/value sets** (`env`) → a child table keyed by `project_id`.
- **Its own domain** (`reactions`) → a separate slice; reactions already have a
  reaction engine to integrate with.

## Surface (per field)

- **API** — extend the projects controller. Field groups get focused routes
  (e.g. `PUT /projects/{id}/agent-config`, `PUT /projects/{id}/env`) rather than
  one mega-PUT, so partial updates are clean and the OpenAPI stays legible.
- **CLI** — typed flags on `ao project` subcommands (e.g.
  `ao project set-config --model --permission`, `ao project env set KEY=VAL`).
- **UI** — a generated typed form per group, driven by the OpenAPI schema.

## Sequencing (one slice per PR)

1. **agentConfig (typed)** — *this PR*. Establishes the typed+validated+surfaced
   pattern end to end.
2. **Project identity scalars** — `default_branch`, `session_prefix` (stop
   hardcoding/deriving them).
3. **Workspace provisioning** — `env`, `symlinks`, `postCreate` (these change
   spawn/workspace wiring, so grouped).
4. **Rules** — `agentRules`, `agentRulesFile`, `orchestratorRules` (consolidate
   the partial `SpawnConfig.AgentRules` path).
5. **Role overrides** — `worker` / `orchestrator` `{agent, agentConfig}`.
6. **Tracker / SCM per-project** — typed blobs with adapter-owned validation.
7. **Per-project reactions** — integrate with the reaction engine.

Each slice is independently shippable and follows the same shape: domain type +
`Validate()` → storage (column or blob or table) → service set/get → API route →
CLI flags → UI form → tests.
