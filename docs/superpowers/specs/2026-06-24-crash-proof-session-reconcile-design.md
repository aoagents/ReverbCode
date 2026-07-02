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

Reconcile iterates `ListAllSessions` and acts per session:

| DB state                      | tmux via `IsAlive(handle)` | Action                                                             |
| ----------------------------- | -------------------------- | ------------------------------------------------------------------ |
| `is_terminated=0`             | alive                      | **Adopt** — no-op, leave live. Agent keeps running.                |
| `is_terminated=0`             | gone                       | `StashUncommitted` (best-effort) -> `MarkTerminated`. No relaunch. |
| `is_terminated=1`             | alive                      | **Reap** — `Destroy` the leaked tmux session.                      |
| `is_terminated=1`, has marker | gone                       | Existing `RestoreAll` restore branch, unchanged.                   |
| `is_terminated=1`, no marker  | gone                       | Leave terminated (user-killed before shutdown; untouched).         |

Adoption is safe and lossless because tmux is the persistence layer: the
detached tmux session survives a daemon crash, and the session's
`runtime_handle_id` (the tmux session name) is in the DB. A matching live
handle means the session genuinely survived; adopting is a no-op.

The **reap** of a terminated-but-still-alive tmux session uses the existing
per-handle `IsAlive` + `Destroy`; no session enumeration is needed because
every leak tied to a session has a DB row (confirmed for the incident: 9, 11,
12 all have rows). Reap must run **before** the restore branch so a restored
session gets a fresh runtime rather than colliding with a leaked tmux of the
same name.

Worktrees: dirty worktrees are **always preserved** (this is why an
intentionally-preserved dirty worktree like session 9 survives — correct, by
design; matches the interactive `Destroy` `ErrWorkspaceDirty` refusal).
Reconcile does not delete worktree directories; worktree lifecycle stays with
the existing teardown/restore/cleanup paths.

### Component 2 — order of operations

`Reconcile` runs three phases over `ListAllSessions`, in order:

1. **Live pass** (`is_terminated=0`): adopt if `IsAlive`, else stash +
   `MarkTerminated`.
2. **Reap pass** (`is_terminated=1` with live tmux): `Destroy` the leaked
   session.
3. **Restore pass**: the existing `RestoreAll` body (terminated + marked
   sessions), unchanged.

Deferred (YAGNI): reaping a tmux session that has **no DB row at all** (a true
orphan). Not observed in the incident and not reachable through normal spawn
(every tmux session is created for a DB-backed session). If it ever appears, it
is a follow-up that adds a `Runtime.ListSessions` enumerator scoped to this
daemon's session-id namespace (so a co-resident AO install's sessions —
observed: `aa-107`, `aa-109` — stay untouched). Out of scope here.

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
- A terminated session whose tmux survived teardown -> reaped (`Destroy`).
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

- Unit: table-test `Reconcile` over each matrix row with a fake runtime whose
  `IsAlive` is scriptable per handle (alive / gone), asserting DB transitions
  (`MarkTerminated`), `StashUncommitted` calls, and runtime `Destroy` (reap)
  calls.
- Unit: assert the live pass adopts (no `Destroy`, no `MarkTerminated`) when
  `IsAlive` is true.
- Integration: extend the sqlite lifecycle test with a seeded
  `is_terminated=0`-but-dead session and a `is_terminated=1`-but-alive session;
  assert the post-reconcile DB state and the reap `Destroy` call.

## Open question (resolved during planning)

Orphan-reap is done per-session via `IsAlive` over DB rows, so there is no
enumeration and no namespace-matching risk in this iteration. The riskier
"reap a tmux session with no DB row" case is deferred (see Component 2,
Deferred), which removes the original namespace-scoping question from scope.
