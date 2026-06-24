# Crash-proof session reconcile — design

Date: 2026-06-24
Status: approved (brainstorming), pending implementation plan
Branch: `feat/crash-proof-session-reconcile`

## Problem

Closing the app can leave orphaned state behind: a detached daemon still
holding its port, live tmux sessions, and worktrees on disk. Observed
directly: app closed, `running.json` pointed at a dead PID, two tmux sessions
(`ao-agents-11`, the orchestrator `ao-agents-12`) still alive, and three
worktrees on disk.

### Root cause

`SaveAndTeardownAll` (the save-on-close teardown) is gated entirely behind
`srv.Run` returning (`backend/internal/daemon/daemon.go:151,163`). `srv.Run`
only returns on a catchable signal (`signal.NotifyContext` for SIGINT/SIGTERM)
or `POST /shutdown`. A **SIGKILL, a crash, or the AppTranslocation mount
vanishing** satisfies none of these: `srv.Run` never returns, so teardown
never runs. The DB confirmed it for the incident: sessions 11 and 12 were
still `is_terminated=0` with no termination or marker writes after the last
activity.

The daemon is spawned `detached` (`frontend/src/main.ts:509`), so on a
non-clean app exit it is orphaned (reparented to launchd), keeps holding the
port and its tmux sessions, and later dies by SIGKILL without ever tearing
down.

### Key principle

You cannot guarantee a clean shutdown. Any fix that only hardens the shutdown
path leaves the SIGKILL/crash hole open. Correctness must come from
**idempotent boot-time reconcile**: every daemon start makes live reality
(tmux + worktrees) match the DB, regardless of how the previous run ended.

## Scope

In scope: a no-leak guarantee. After any app exit (clean, force-quit, crash),
the next boot reconciles so there are no orphaned daemon/tmux/worktrees, and
every live session is either adopted or cleanly terminated.

Out of scope (deliberately unchanged — separate decision):

- Orchestrator re-spawn-vs-restore policy and stale `session_worktrees` marker
  cleanup (the "orchestrator spam" bug).
- Auto-relaunching crash-killed agents. Reconcile preserves work and marks
  terminated; it never spawns a new agent.

## Design

### Component 1 — `Manager.Reconcile(ctx)` (daemon side, the core)

A single idempotent pass that **replaces** the bare `RestoreAll` call at
`daemon.go:147`, run before the server starts serving. It folds the existing
restore logic in as one branch. Iterating `ListAllSessions`:

| DB state                          | tmux via `IsAlive(handle)` | Action                                                              |
| --------------------------------- | -------------------------- | ------------------------------------------------------------------ |
| `is_terminated=0`                 | alive                      | **Adopt** — no-op, leave live. Agent keeps running.                |
| `is_terminated=0`                 | gone                       | `StashUncommitted` (best-effort) -> `MarkTerminated`. No relaunch. |
| `is_terminated=1`, has marker     | (n/a)                      | Existing `RestoreAll` restore branch, unchanged.                   |
| `is_terminated=1`, no marker      | (n/a)                      | Leave terminated (user-killed before shutdown; untouched).         |

Adoption is safe and lossless because tmux is the persistence layer: the
detached tmux session survives a daemon crash, and the session's
`runtime_handle_id` (the tmux session name) is in the DB. A matching live
handle means the session genuinely survived; adopting is a no-op.

After the per-session pass, **orphan-reap** using a new `Runtime.ListSessions`.
The reap is **scoped to this daemon's session-id namespace** (the project
session-id prefix, e.g. `ao-agents-`): a tmux name outside that namespace is
never touched, which is what keeps a co-resident AO install's sessions
(observed: `aa-107`, `aa-109` from a different install on the same host) safe.
Within the namespace, for every live tmux session whose name maps to **no
`is_terminated=0` DB row** (a terminated row, or no row at all) -> `Destroy`
it.

Worktrees: a terminated session's worktree is pruned **only if it is clean and
registered-but-dead**; **dirty worktrees are always preserved** (this is why an
intentionally-preserved dirty worktree like session 9 survives — correct, by
design; matches the interactive `Destroy` `ErrWorkspaceDirty` refusal).

### Component 2 — `Runtime.ListSessions(ctx) ([]string, error)` (port addition)

The only new surface on the `ports.Runtime` interface
(`backend/internal/ports/outbound.go`).

- tmux: `tmux list-sessions -F '#{session_name}'`. An empty list (no server /
  no sessions) is returned as an empty slice and **no error**, mirroring the
  existing `has-session` missing-output handling.
- conpty: return an empty slice (no persistent enumeration model). Reconcile's
  orphan-reap is therefore a tmux-only effect, which is correct: only tmux
  sessions outlive the daemon.

### Component 3 — Frontend "replace wedged orphan" branch

The healthy-attach path already exists: `inspectExistingDaemon` +
`resolveDaemonFromPort` (`frontend/src/main.ts:457-485`) attach to a healthy
existing daemon. The gap is the failure branch. Add: when the port is held but
the daemon is unhealthy / identity-mismatched / PID-dead-but-port-held,
SIGTERM the process group, wait for the port to free, clear the stale
`running.json`, then spawn fresh (which runs Reconcile). A healthy orphan is
reconnected exactly as today, untouched.

## Behaviour for the observed incident

- 11 & 12 (alive tmux) -> **adopted**, nothing lost.
- A future crash where tmux also died -> work stashed, marked terminated, no
  orphan left.
- Orphan daemon on next launch -> reused if healthy, else killed + replaced.
- Orphan tmux with no live owner -> reaped.
- Dirty worktrees (like 9) -> preserved.

## Error handling

- Per-session reconcile failures are logged and never abort the pass (same
  pattern as `SaveAndTeardownAll` / `RestoreAll`).
- `Reconcile` is best-effort and must never block boot: a failure is logged,
  boot continues (same contract as the current `RestoreAll` call site).
- `StashUncommitted` on a crash-dead worktree is best-effort; a failure logs
  and still proceeds to `MarkTerminated` (no work is destroyed — the worktree
  stays on disk).
- Orphan-reap `Destroy` failures are logged and do not abort the loop.

## Testing

- Unit: table-test `Reconcile` over each matrix row with a fake runtime
  (alive / gone / orphan), asserting DB transitions and runtime `Destroy`
  calls.
- Unit: `ListSessions` argument building and missing-output parsing, mirroring
  the existing tmux `has-session` tests.
- Integration: extend the sqlite lifecycle test with a seeded
  `is_terminated=0`-but-dead session plus an orphan tmux name; assert the
  post-reconcile DB state and the kill calls.

## Open question flagged for review

Orphan-reap namespace scoping: the design reaps in-namespace tmux names that
have no live DB owner, including names with no DB row at all. The namespace
prefix (e.g. `ao-agents-`) is what protects co-resident installs. Confirm the
prefix is derivable reliably at reconcile time (from the project's session
prefix). If the no-DB-row case ever proves too broad, the safe fallback is to
reap only names resolving to a known-but-terminated DB row and merely log
in-namespace names with no row.
