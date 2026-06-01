# Agent Orchestrator backend architecture

The backend is a long-running Go daemon that supervises coding-agent sessions.
The current model is intentionally small: session rows persist only durable facts,
and display status is derived at read time.

## Mental model

```
OBSERVE external facts → UPDATE durable facts → DERIVE display status / ACT
```

The durable session facts are:

- `activity_state` — what the agent last reported or what the runtime observer
  can safely conclude (`active`, `ready`, `idle`, `waiting_input`, `blocked`,
  `exited`).
- `is_terminated` — whether the session should be treated as over.
- PR facts in the `pr`, `pr_checks`, and `pr_comment` tables.

The UI status is not stored. `domain.DeriveStatus` computes it from the session
record plus PR facts.

## Package layout

```
backend/internal/domain       shared vocabulary and display-status derivation
backend/internal/ports        inbound/outbound interfaces
backend/internal/session      explicit mutations: spawn, kill, restore, send, cleanup
backend/internal/lifecycle    runtime/activity/spawn/termination session fact reducer
backend/internal/pr           PR observation ingestion
backend/internal/storage      SQLite persistence and DB-triggered CDC
backend/internal/cdc          change-log poller and broadcaster
backend/internal/httpd        daemon HTTP surface
backend/internal/terminal     WebSocket terminal multiplexer
backend/internal/adapters     Zellij/git-worktree/GitHub adapters
```

## Status derivation

`session.Manager` selects the display PR from all PR snapshots for a session, then
`domain.DeriveStatus(session, prFacts)` applies this rough precedence:

1. `is_terminated` → `terminated`, except merged PRs display `merged`.
2. `activity_state=waiting_input` → `needs_input`.
3. `activity_state=blocked` → `stuck`.
4. Open PR facts drive PR pipeline statuses: `ci_failed`, `draft`,
   `changes_requested`, `mergeable`, `approved`, `review_pending`, `pr_open`.
5. `activity_state=active` → `working`.
6. Everything else → `idle`.

## Lifecycle manager

`lifecycle.Manager` is the write path for session lifecycle facts and lifecycle-owned agent nudges:

- runtime observations can mark a session terminated only when runtime and
  process are both clearly dead and recent activity does not contradict that;
  failed/unknown probes do not persist a special state.
- activity signals update `activity_state`; `exited` also marks the session
  terminated.
- PR observations do not write PR rows here, but after the PR service persists
  them lifecycle sends actionable agent nudges for CI failures, review feedback,
  and merge conflicts.

## PR manager

`pr.Manager` records SCM observations into the PR/check/comment tables, then
forwards the observation to lifecycle for agent nudges. A merged PR marks the
owning session terminated through the lifecycle manager; other PR facts are
consumed at read time for display status.

## Session manager

`session.Manager` performs explicit user mutations:

- `Spawn` creates a row, creates workspace/runtime resources, and reports the
  handles to the lifecycle manager.
- `Kill` marks the row terminated, then tears down runtime/workspace resources.
- `Restore` relaunches a terminated session and clears `is_terminated` via the
  spawn-completed path.
- `List`/`Get` attach the derived display status.

## Persistence and CDC

SQLite is the durable store. User-visible table changes are captured by database
triggers into `change_log`; the Go store does not manually emit CDC events. A
poller tails `change_log` and publishes live events to in-process subscribers.

## Load-bearing rules

- Do not store display status.
- Keep session status facts small: `activity_state`, `is_terminated`, and PR
  facts are the durable inputs.
- Do not treat failed probes as death.
- Do not force-delete registered dirty worktrees.
