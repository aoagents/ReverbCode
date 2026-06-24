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

#### 2. Recreate endpoint (orchestrator-only)
- Route: `POST /api/v1/sessions/{sessionId}/recreate`, registered next to the
  other session routes in `httpd/controllers/sessions.go`, handled by a new
  `SessionsController.recreate` that calls `Svc.RecreateOrchestrator`.
- Service method `RecreateOrchestrator(ctx, id) (domain.Session, error)` wraps
  the manager method and runs its error through `toAPIError`.
- Manager method `RecreateOrchestrator(ctx, id domain.SessionID)
  (domain.SessionRecord, error)`:
  1. `GetSession`; 404 (`ErrNotFound`) if absent.
  2. Validate `rec.Kind == domain.KindOrchestrator`; if not, a typed Conflict
     (`SESSION_NOT_ORCHESTRATOR`, "Only orchestrator sessions can be recreated").
  3. Validate `rec.IsTerminated`; if still live, a typed Conflict
     (reuse/`ErrNotRestorable`-style: "Session is still running").
  4. Validate `meta.Branch != ""` (else `ErrIncompleteHandle`).
  5. Force-clean any stale worktree dir for the branch, then attach a clean
     worktree on the EXISTING branch (reuse `workspace.Restore`, which already
     does the existing-branch `worktree add`).
  6. Launch a FRESH orchestrator agent as a **new session** on that branch:
     create a new seed session row (kind=orchestrator, same project, branch =
     the existing branch), build the orchestrator system prompt + argv, then
     `runtime.Create` + `lcm.MarkSpawned` (reuse Spawn's launch tail; do not
     duplicate its logic — factor a shared helper if the tail is not already
     callable).
  7. The old session row stays terminated. Return the NEW session record.
  - **Orchestrator uniqueness:** recreate must honor whatever
    one-active-orchestrator-per-project rule Spawn enforces (see
    `activeOrchestratorSessionID`). Recreating from a terminated orchestrator is
    allowed; if a DIFFERENT orchestrator is already live for the project,
    recreate returns the same typed conflict Spawn would, rather than creating a
    second live orchestrator.
- The new endpoint requires regenerating the OpenAPI spec (`npm run api`) and
  will update the `httpd` spec-drift tests. This is expected and correct for a
  new route (unlike the prior lifecycle plan, which added no routes).

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
    orchestrator"** → `POST /api/v1/sessions/{id}/recreate` with a loading
    state; on success, invalidate workspace queries and select the returned new
    session; "Cancel" closes.
  - If `kind === "worker"`: explanatory text + "Close" only (no recreate).
- Detect the code via the API error body `code === "SESSION_NOT_RESUMABLE"`
  (same envelope `apiErrorMessage`/error-shape the renderer already reads).

## Data flow

```
User clicks "Restore session"
  -> POST /sessions/{id}/restore
       restorable     -> 200, terminal attaches
       not resumable  -> 409 SESSION_NOT_RESUMABLE
                          -> popup opens
                               orchestrator -> "Create new orchestrator"
                                  -> POST /sessions/{id}/recreate
                                       -> clean worktree, attach existing branch,
                                          launch fresh orchestrator as NEW session
                                       -> 200, select new session
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
  `agent_session_id`+`prompt` returns `ErrNotResumable`; `RecreateOrchestrator`
  on a terminated orchestrator attaches the existing branch and returns a new
  orchestrator session; rejects a live session, a worker, and a missing branch
  with the typed errors.
- **Backend service:** `toAPIError(ErrNotResumable)` → 409 `SESSION_NOT_RESUMABLE`.
- **Backend httpd:** the new route appears in the OpenAPI spec; spec-drift tests
  green after `npm run api`.
- **Frontend:** typecheck green; the `restoreSession` handler routes a
  `SESSION_NOT_RESUMABLE` response to the dialog and a success to attach; the
  dialog shows the orchestrator create button only for `kind === "orchestrator"`.
- **Manual:** on the packaged build, terminate an orchestrator that has no
  resume state, click Restore, confirm the popup appears (not a 500), click
  "Create new orchestrator", confirm a fresh orchestrator launches on the same
  branch with history intact.

## Files touched

- `backend/internal/session_manager/manager.go` — `ErrNotResumable`,
  `RecreateOrchestrator`, shared launch-tail helper if needed.
- `backend/internal/service/session/service.go` — `toAPIError` case,
  `RecreateOrchestrator` service method.
- `backend/internal/httpd/controllers/sessions.go` (+ `dto.go`) — route +
  handler + response DTO.
- `backend/internal/httpd/apispec/...` + generated spec via `npm run api`.
- `frontend/src/renderer/components/TerminalPane.tsx` — handler branch.
- `frontend/src/renderer/components/RestoreUnavailableDialog.tsx` — new dialog.

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
