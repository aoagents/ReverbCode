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

## Metadata Keys

Hook callbacks persist these normalized keys in the session metadata JSON blob:

- `agentSessionId`: native agent session id.
- `title`: display title, derived from the first user prompt hook for the session.
- `summary`: display summary, derived from the final assistant message exposed to the stop hook.

The original spawn prompt may remain in metadata as `prompt` for launch/debug fallback, but `title` is the preferred display title once hook metadata lands.

## Hook Methodology

Agent adapters install hooks into the worktree-local config owned by the native agent.

Hook callbacks run through hidden AO CLI commands:

```text
ao hooks <agent-adapter> <event>
```

The callback:

1. Reads the native hook JSON payload from stdin.
2. Reads the AO session id from `AO_SESSION_ID`.
3. Opens the AO SQLite store (`ao.db`) in the data dir — `AO_DATA_DIR`, default `<user config dir>/agent-orchestrator/data`.
4. Merges normalized metadata into the matching session row.
5. Publishes `session.updated` when metadata changed.
6. Prints `{}` and exits 0 for successful no-op cases, including non-AO sessions or missing rows.

The spawn engine inserts the AO session row before launching the durability provider so early startup hooks can update an existing row. If launch fails after insertion, spawn deletes the row during rollback.

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
