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
| Config | `github.com/spf13/viper` | Planned | Standard pairing with Cobra/pflag for CLI, env, and file-based configuration. |
| CLI | `cobra` | Planned | Standard command structure for daemon startup, diagnostics, and admin commands. |
| Logging | `log/slog` | Planned | Stdlib structured logging before adding another logging dependency. |
| Testing | stdlib `testing` | Implemented | Keep pure domain logic and adapter contracts easy to test. |
| Test assertions | `testify/require` | Planned | Concise assertions for higher-level adapter and integration tests. |
| Packaging | `goreleaser` | Planned | Cross-platform release automation, checksums, and future Homebrew support. |

## Pending decisions

### SQLite driver

Use `github.com/ncruces/go-sqlite3/driver` first.

| Driver | When to choose it | Tradeoff |
|--------|-------------------|----------|
| `github.com/ncruces/go-sqlite3/driver` | Default V1 choice. | `database/sql` driver with an easier no-CGO distribution story; validate against AO's WAL/outbox workload. |
| `github.com/mattn/go-sqlite3` | Fallback if compatibility or performance requires it. | Mature and widely used, but cross-compilation and toolchain setup are harder. |

Keep the driver behind `database/sql` so the persistence layer can switch
drivers if validation exposes compatibility or performance issues.

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
