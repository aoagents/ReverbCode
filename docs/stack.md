# AO technical stack

This is the source of truth for library and runtime choices in the AO rewrite.
Keep this document about durable technology decisions; use `status.md` for
implementation progress and `architecture.md` for component behavior and
invariants.

## Principles

- Prefer the Go standard library until a small dependency clearly earns its
  place.
- Keep the backend daemon boring: explicit process control, explicit SQL,
  narrow adapters, and observable failure modes.
- Shell out where AO needs the user's real developer-machine behavior, especially
  for Git and terminal multiplexers.
- Keep high-volume terminal output out of SQLite; store structured state in the
  database and stream/log payload-heavy data separately.

## Accepted stack

| Area | Decision | Status | Rationale |
|------|----------|--------|-----------|
| Backend language | Go 1.22 | Implemented | Small daemon, strong stdlib, easy local distribution. |
| Backend core | Go stdlib | Implemented | Domain, lifecycle, session, and adapter contracts should stay dependency-light. |
| Frontend shell | Electron + TypeScript | Implemented | Local desktop control plane paired with the daemon. |
| Runtime adapters | `tmux` and `zellij` CLIs via `os/exec` | Implemented | Terminal multiplexers fit long-running sessions, attach/debug workflows, and adapter isolation. |
| Git/worktrees | `git` CLI via `os/exec` | Implemented | Uses real repo behavior, credentials, hooks, LFS, submodules, and user config. |
| HTTP API | `net/http` + `github.com/go-chi/chi/v5` | Planned / branch work exists | Lightweight, idiomatic router without committing AO to a large web framework. |
| WebSocket | `github.com/coder/websocket` | Planned | Modern small WebSocket library for event and terminal streaming. |
| Storage | SQLite in WAL mode | Planned | Local daemon, single writer, many dashboard/API reads, no external DB setup. |
| SQL access | `database/sql` + `sqlc` | Planned | Hand-written SQL with generated typed methods. |
| Migrations | `goose` | Planned | Simple SQL migrations for an embedded/local database. |
| Config | `koanf` | Planned | Explicit config loading without the heavier Cobra/Viper coupling. |
| CLI | `cobra` | Planned | Standard command structure for daemon startup, diagnostics, and admin commands. |
| Logging | `log/slog` | Planned | Stdlib structured logging before adding another logging dependency. |
| Testing | stdlib `testing` | Implemented | Keep pure domain logic and adapter contracts easy to test. |
| Test assertions | `testify/require` | Planned | Concise assertions for higher-level adapter and integration tests. |
| Packaging | `goreleaser` | Planned | Cross-platform release automation, checksums, and future Homebrew support. |

## Pending decisions

### SQLite driver

Use one of:

| Driver | When to choose it | Tradeoff |
|--------|-------------------|----------|
| `github.com/mattn/go-sqlite3` | Choose if CGO is acceptable. | Mature and widely used, but cross-compilation and toolchain setup are harder. |
| `modernc.org/sqlite` | Choose if pure-Go distribution matters more. | Easier static/cross-platform builds, but should be validated against AO's WAL/outbox workload. |

Default recommendation: start with `mattn/go-sqlite3` if CGO is acceptable;
switch to `modernc.org/sqlite` if release packaging or user install friction
becomes the blocker.

Required SQLite setup:

```sql
PRAGMA journal_mode = WAL;
PRAGMA busy_timeout = 5000;
PRAGMA foreign_keys = ON;
PRAGMA synchronous = NORMAL;
```

### Process runtime

The default V1 runtime is a terminal-multiplexer adapter (`tmux` or `zellij`).
A direct PTY runtime using `github.com/creack/pty` is a later option behind the
existing runtime port, not the default V1 runtime.

## Explicitly avoided for V1

| Avoid | Reason |
|-------|--------|
| GORM | AO needs explicit transactional SQL and outbox writes. |
| Gin/Fiber | `net/http` + `chi` is enough for a local daemon API. |
| `go-git` as the primary Git engine | AO should match installed Git behavior, credentials, hooks, LFS, submodules, and user config. |
| Temporal / NATS / Kafka / Redis | V1 is a local daemon with SQLite and JSONL delivery, not a distributed control plane. |
| Full plugin framework | Keep adapter interfaces narrow until product needs justify a plugin runtime. |
| Multi-sink outbox | Start with one durable local delivery path; add fan-out later if needed. |

## Architecture mapping

```txt
Go daemon
  net/http + chi
  coder/websocket
  tmux/zellij runtime adapters via os/exec
  git worktree adapter via git CLI
  SQLite via database/sql
  sqlc
  goose
  slog
  cobra CLI
  JSONL files for terminal logs and delivery streams
```

This stack supports the current architecture: one LCM writer, SQLite current
state, change log and outbox, JSONL delivery streams, terminal sessions, and
real Git worktrees.
