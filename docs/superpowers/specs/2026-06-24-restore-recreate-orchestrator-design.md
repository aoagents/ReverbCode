# Design: Graceful Restore + Post-Failure Orchestrator Recreate

## Problem

Clicking "Restore session" on a terminated session that has no resumable state
returns an opaque **HTTP 500** and the UI shows "Internal server error". Root
cause, traced through the running build:

- `manager.Restore` (`backend/internal/session_manager/manager.go:479-480`)
  returns a **plain** error when a session has neither an agent session id nor a
  prompt:
  ```go
  if meta.AgentSessionID == "" && meta.Prompt == "" {
      return ..., fmt.Errorf("restore %s: nothing to resume from", id)
  }
  ```
- `toAPIError` (`backend/internal/service/session/service.go:444`) maps known
  sentinels (`ErrNotRestorable`, `ErrIncompleteHandle`, ...) to clean 4xx codes,
  but an unrecognized error "passes through and surfaces as a 500" (its own
  comment). "nothing to resume from" is not a sentinel, so the user gets a 500.

Observed on `ao-agents-8`: a terminated **orchestrator** with empty
`agent_session_id` and empty `prompt` (a stale pre-lifecycle-feature orphan).
The branch `ao/ao-agents-orchestrator` still exists with its committed history;
only the resumable agent state is gone.

This is a **pre-existing** bug (the single-session restore endpoint predates the
session-lifecycle feature). It is now visible because terminating such a session
makes the UI offer its Restore button.

## Goals

1. A restore that cannot succeed returns a clear, typed client error, never a 500.
2. When restore is confirmed impossible for an **orchestrator**, the user is
   offered, via a **popup that appears only after clicking Restore**, the option
   to create a fresh orchestrator on the same branch (preserving committed
   history), cleaning the old worktree.
3. Restore is offered/attempted normally for sessions that CAN be restored; the
   recreate path never fires unless a restore attempt was made and the backend
   confirmed it is not resumable. No orchestrator spam when restore works.

## Non-Goals

- Workers get the clear error + popup explanation, but **no** recreate action
  (scope decision: orchestrators only).
- No change to how restorable sessions resume (the existing resume path stays
  behaviorally unchanged).
- No upfront `restorable` flag on the session DTO: the flow is driven by the
  restore attempt's response, so a precomputed flag is unnecessary (YAGNI).

## Core reframe

Two distinct operations on a terminated session share worktree machinery but
differ at launch:

- **Restore** = re-attach a worktree on the existing branch + **resume** the
  agent (requires `agent_session_id` or `prompt`).
- **Recreate orchestrator** = re-attach a worktree on the existing branch +
  launch a **fresh** orchestrator agent (no resume state needed).

`worktree add` has two arg builders in
`backend/internal/adapters/workspace/gitworktree/commands.go`:
`worktreeAddBranchArgs` (existing branch, no `-b`, used by `Restore`) and
`worktreeAddNewBranchArgs` (`-b`, new branch, used by `Create`/Spawn). Recreate
must REUSE the existing branch, so it goes through the existing-branch attach
(the `Restore` path), NOT Spawn's `-b` path.

## Design

### Backend

#### 1. Typed error for un-resumable restore (fixes the 500)

- Add sentinel in `session_manager` (next to the existing sentinels near
  `manager.go:25`):
  ```go
  ErrNotResumable = errors.New("session: nothing to resume from")
  ```
- Use it at `manager.go:480`:
  ```go
  return domain.SessionRecord{}, fmt.Errorf("restore %s: %w", id, ErrNotResumable)
  ```
- Map it in `toAPIError` (`service/session/service.go`), alongside the sibling
  cases, as a **409**:
  ```go
  case errors.Is(err, sessionmanager.ErrNotResumable):
      return apierr.Conflict("SESSION_NOT_RESUMABLE",
          "This session has no saved agent session or prompt to resume from", nil)
  ```

#### 2. Recreate: REUSE the existing `POST /api/v1/orchestrators` (clean=true)

**Discovery during planning:** the recreate capability already ships. No new
endpoint or manager method is needed.

- `SessionsController.spawnOrchestrator` already handles `POST /api/v1/orchestrators`
  with body `{projectId, clean}` (`httpd/controllers/sessions.go`).
- `Service.SpawnOrchestrator(ctx, projectID, clean)`
  (`service/session/service.go:263`): when `clean` is true it kills any active
  orchestrators for the project, then `Spawn(SpawnConfig{ProjectID, Kind:
orchestrator})`.
- `Spawn` with no branch defaults to the canonical orchestrator branch
  `ao/<prefix>-orchestrator` (`defaultSessionBranch`). That is the SAME branch
  the dead orchestrator used.
- `workspace.Create` -> `addWorktree`
  (`adapters/workspace/gitworktree/workspace.go`) already detects an EXISTING
  local branch (`refExists("refs/heads/"+branch)`) and attaches it with the
  no-`-b` `worktreeAddBranchArgs` (preserving committed history); it only uses
  `-b` for a genuinely new branch, and refuses with `ErrBranchCheckedOutElsewhere`
  (409) if the branch is live in another worktree.

So "create a new orchestrator on the same branch, cleaning the old worktree" =
`POST /api/v1/orchestrators {projectId, clean:true}`. The `clean` kill frees the
dead orchestrator's worktree; the re-spawn reattaches the existing branch. The
old session row stays terminated; a new orchestrator session id is returned.
Orchestrator uniqueness is already enforced by the `clean` kill-then-spawn rule.

The ONLY backend change in this feature is item #1 (the typed error). No new
route, no `RecreateOrchestrator`, no OpenAPI/spec regen.

### Frontend

`frontend/src/renderer/components/TerminalPane.tsx`:

- The **"Restore session"** button stays on every terminated non-reviewer
  session (the existing `canRestoreSession` trigger is unchanged).
- `restoreSession` handler, after `POST /api/v1/sessions/{id}/restore`:
  - success → invalidate workspace queries + attach (existing behavior).
  - error whose API code is **`SESSION_NOT_RESUMABLE`** → open a new dialog
    component instead of showing the inline error.
  - any other error → existing inline error display.
- New `RestoreUnavailableDialog` component (Radix Dialog, mirroring
  `NewTaskDialog.tsx`; primitives from `components/ui/*`):
  - Title: "Session can no longer be restored".
  - Body: explains there is no saved agent session/prompt to resume from.
  - If the session `kind === "orchestrator"`: primary button **"Create new
    orchestrator"** → calls the existing `spawnOrchestrator` helper
    (`frontend/src/renderer/lib/spawn-orchestrator.ts`) extended with a `clean`
    argument: `spawnOrchestrator(projectId, true)` → `POST /api/v1/orchestrators
{projectId, clean:true}`, with a loading state; on success, invalidate
    workspace queries and select the returned new orchestrator id; "Cancel"
    closes.
  - If `kind === "worker"`: explanatory text + "Close" only (no recreate).
- Detect the code via the API error body `code === "SESSION_NOT_RESUMABLE"`
  (same envelope `apiErrorMessage`/error-shape the renderer already reads).
- `spawn-orchestrator.ts` gains an optional `clean = false` parameter passed
  through to the request body; the existing single-arg call sites are unchanged.

## Data flow

```
User clicks "Restore session"
  -> POST /sessions/{id}/restore
       restorable     -> 200, terminal attaches
       not resumable  -> 409 SESSION_NOT_RESUMABLE
                          -> popup opens
                               orchestrator -> "Create new orchestrator"
                                  -> POST /api/v1/orchestrators {projectId, clean:true}
                                       (existing endpoint: kills active orchestrator,
                                        re-spawns on canonical branch, reattaches
                                        existing branch with history)
                                       -> 201, select new orchestrator
                               worker -> explanatory close-only popup
```

## Error handling

- All restore/recreate failures are typed `apierr` values → correct 4xx, never a
  500 for a client-actionable condition.
- Recreate is best-effort-validated up front (kind, terminated, branch present)
  so the common rejections are clean 409s, not deep wrapped errors.
- Worktree attach failures during recreate surface as the existing workspace
  error kinds (e.g. branch-checked-out-elsewhere) already mapped in `toAPIError`.

## Testing

- **Backend unit (session_manager):** restore of a terminated session with empty
  `agent_session_id`+`prompt` returns `ErrNotResumable`.
- **Backend service:** `toAPIError(ErrNotResumable)` → 409 `SESSION_NOT_RESUMABLE`.
- **Frontend:** typecheck green; the `restoreSession` handler routes a
  `SESSION_NOT_RESUMABLE` response to the dialog and a success to attach; the
  dialog shows the orchestrator create button only for `kind === "orchestrator"`;
  `spawnOrchestrator(projectId, true)` sends `clean:true`.
- **Manual:** on the packaged build, terminate an orchestrator that has no
  resume state, click Restore, confirm the popup appears (not a 500), click
  "Create new orchestrator", confirm a fresh orchestrator launches on the same
  branch with history intact.

## Files touched

- `backend/internal/session_manager/manager.go` — `ErrNotResumable` sentinel +
  use it at the "nothing to resume from" return.
- `backend/internal/service/session/service.go` — `toAPIError` case for
  `ErrNotResumable` → 409 `SESSION_NOT_RESUMABLE`.
- `frontend/src/renderer/lib/spawn-orchestrator.ts` — optional `clean` param.
- `frontend/src/renderer/components/TerminalPane.tsx` — restore handler routes
  `SESSION_NOT_RESUMABLE` to the dialog.
- `frontend/src/renderer/components/RestoreUnavailableDialog.tsx` — new dialog.

No new backend route, manager method, or OpenAPI regeneration: the recreate
reuses the existing `POST /api/v1/orchestrators` (clean=true) path.

## Constraints (binding)

- No em dashes or en dashes anywhere (prose, comments, commit messages).
- Renderer clones the agent-orchestrator web app; build the dialog from shadcn
  primitives (`components/ui/*`) and the Radix Dialog pattern already used by
  `NewTaskDialog.tsx`. (See `DESIGN.md`.)
- App state under `~/.ao` only (not directly touched here).
- Do not hand-edit generated sqlc or OpenAPI output; regenerate via the npm
  scripts.
- The existing resume path and the interactive dirty-refusal removal path stay
  behaviorally unchanged.
