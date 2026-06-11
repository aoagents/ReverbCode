# Agent Adapter PRD

## Goal

Agent adapters let AO run and observe different CLI coding agents without hardcoding agent-specific behavior into the spawn engine. Every CLI coding agent must implement the contract in `backend/internal/ports/agent.go`.

The important current slice is hook-derived session info. AO should know a running worker's native agent session id, title, and summary from agent hooks installed in the per-session worktree, not from scanning agent transcript/cache files.

## Current Decisions

- AO only needs to derive session info for AO-managed sessions.
- Hook installation happens at worktree/session creation time.
- `SessionInfo` reads normalized metadata persisted in AO's session store.
- `SessionInfo` must not infer display info by reading agent transcript/cache files.
- `SummaryIsFallback` is removed from `ports.SessionInfo`.
- `TranscriptPath` is removed from `ports.SessionInfo`.
- `Title` and `Summary` are both first-class fields.
- `Title` is derived from the user prompt hook.
- `Summary` is derived from the stop/final assistant hook.
- Agent adapter `Metadata` should stay nil/empty unless an adapter has a real extra field that does not belong in the normalized contract.

## Agent Contract

The shared contract lives in `backend/internal/ports/agent.go`.

Required adapter behavior:

- `GetConfigSpec` describes user-facing agent config.
- `GetLaunchCommand` builds the native agent command.
- `GetPromptDeliveryStrategy` says whether the prompt is passed in argv or sent after launch.
- `GetAgentHooks` installs or merges AO hooks into the agent's workspace-local hook config.
- `GetRestoreCommand` builds a native resume command when restore is supported.
- `SessionInfo` returns normalized metadata:
  - `AgentSessionID`
  - `Title`
  - `Summary`
  - optional adapter-specific `Metadata`

Implementation layout:

- Agent-specific hook installation should live beside the agent adapter in `backend/internal/adapters/agent/<agent>/hooks.go`; the hook commands are defined in code, not embedded template files.
- Launch, restore, and session-info behavior can stay in the main agent implementation unless the file grows enough to justify another split.
- Every file an adapter writes into the session worktree must be covered by a sibling self-ignoring `.gitignore` written via `hookutil.EnsureWorkspaceGitignore`. Hook files are untracked, and `git worktree remove` (never run with `--force`) refuses on any untracked file — an uncovered hook file makes every session workspace permanently undeletable. The registry conformance test (`registry.TestGetAgentHooksFootprintIsGitignored`) enforces this for all adapters.

## Metadata Keys

Hook callbacks persist these normalized keys in the session metadata JSON blob:

- `agentSessionId`: native agent session id.
- `title`: display title, derived from the first user prompt hook for the session.
- `summary`: display summary, derived from the final assistant message exposed to the stop hook.

The original spawn prompt may remain in metadata as `prompt` for launch/debug fallback, but `title` is the preferred display title once hook metadata lands.

## Hook Methodology

Agent adapters install hooks into the worktree-local config owned by the native agent.

Codex is the exception: Codex (0.136+) only loads project-local `.codex/` hook config from trusted directories, and for linked git worktrees it sources hook declarations from the matching folder in the root checkout — never from AO's per-session worktree. The Codex adapter therefore passes its hooks on the launch command as `-c 'hooks.<Event>=[...]'` session-flag config (plus `--dangerously-bypass-hook-trust`, since session-flag hooks have no persisted trust hash), and marks the worktree as a trusted project for the invocation with `-c 'projects={"<worktree>"={trust_level="trusted"}}'` so spawns into never-before-trusted repos don't hang on Codex's interactive directory-trust prompt. Its `GetAgentHooks` writes nothing; it only strips entries older AO versions left in the worktree-local `.codex/hooks.json`.

Hook callbacks run through hidden AO CLI commands:

```text
ao hooks <agent-adapter> <event>
```

The callback:

1. Reads the native hook JSON payload from stdin.
2. Reads the AO session id from `AO_SESSION_ID` (exits 0 immediately for non-AO sessions).
3. Derives a normalized activity state from the agent + event (`activitydispatch.Derive`); events with no activity meaning report nothing.
4. POSTs the state to the daemon at `POST /api/v1/sessions/{id}/activity`; the daemon owns the store and fans out `session.updated` via CDC.
5. Always exits 0 — a failed delivery must never break the user's agent. Failures are appended to `hooks.log` under `AO_DATA_DIR` and surfaced by the `hooks-log` check in `ao doctor`.

The daemon also records the FIRST callback per spawn/restore (`first_signal_at`); a live session that has never signaled past a grace period derives the `no_signal` display status instead of a confident `idle`, so a broken hook pipeline is visible on the dashboard. The downgrade only applies to harnesses with a registered activity deriver (`activitydispatch.SupportsHarness`, injected into the session service at daemon wiring) — for a hook-less adapter, permanent silence is normal and stays `idle`. Known limitation: neither Codex nor Claude Code derives an activity state from `SessionStart`, so a restored session the user never prompts has nothing to signal and shows `no_signal` once the grace passes; a receipt-only session-start signal would close that gap.

Persisting hook-derived metadata (`agentSessionId`, `title`, `summary`) into the session row is **not implemented yet** — until it is, adapters whose restore needs the native session id (e.g. `codex resume`) fall back to a fresh launch.

The spawn engine inserts the AO session row before launching the durability provider so early startup hooks can update an existing row. If launch fails after insertion, spawn deletes the row during rollback.

The hook commands are a bare `ao hooks ...` on purpose: worktree-committed hook files stay machine-portable, and adapters recognize their own entries by command prefix for install/dedup/uninstall. To make the bare `ao` resolve to the daemon that installed the hooks (not a foreign or legacy `ao` earlier on the user's PATH), the session manager pins each spawned session's `PATH` with the daemon executable's directory first. When the pin cannot be applied (executable unresolvable or not named `ao`), the daemon logs a warning at spawn. Hook delivery failures are best-effort appended to `hooks.log` under `AO_DATA_DIR` (agents swallow hook stderr), and `ao doctor` warns when the `ao` on PATH is not the running binary.

## Restore Boundary

Session display info and native restore are separate concerns.

Some agents may still need transcript-derived or deterministic native ids for `GetRestoreCommand` until restore is redesigned for that agent. Do not remove restore support just because `SessionInfo` stops reading transcripts.

For `SessionInfo`, transcript/cache files are not an acceptable source of title or summary.

## UI And Events

The workspace adapter prefers:

- `metadata.title` as session title.
- `metadata.summary` as session description.
- `metadata.prompt` only as fallback.

Hook metadata changes publish `session.updated`. The frontend listens to `session.created`, `session.terminated`, and `session.updated` and invalidates the workspace query.

## Acceptance Criteria

Agent adapter behavior:

- Agent hook installation preserves user hooks and deduplicates AO hooks.
- Hook callbacks persist native session id, title, and summary.
- `SessionInfo` returns normalized fields from persisted metadata.
- `SessionInfo` does not read transcripts or caches for title/summary.
- Adapter-specific metadata stays nil/empty unless a concrete feature requires it.

Engine and UI:

- Spawn installs hooks before launching the native agent.
- The session row exists before launch so hooks can merge metadata.
- Launch failure after row insertion deletes the row.
- Metadata updates publish `session.updated`.
- The dashboard refreshes title/summary without a manual reload.

Verification:

```sh
(cd backend && go test ./...)
(cd frontend && npm run typecheck)
```
