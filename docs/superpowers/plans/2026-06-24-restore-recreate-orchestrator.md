# Restore Recreate Orchestrator Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn the opaque 500 on restoring an un-resumable session into a typed 409, and add a popup (shown only after a failed restore) that offers to recreate a fresh orchestrator on the same branch.

**Architecture:** One small backend change (a typed sentinel error + its 409 mapping) plus a frontend popup. The recreate action reuses the EXISTING `POST /api/v1/orchestrators {clean:true}` endpoint, which already kills the dead orchestrator and re-spawns one on the canonical branch (reattaching the existing branch with history). No new backend route, manager method, or OpenAPI regeneration.

**Tech Stack:** Go backend (session_manager + service/session), React + TypeScript renderer (Radix Dialog, openapi-fetch api client, vitest).

## Global Constraints

- No em dashes or en dashes anywhere (prose, comments, commit messages). Use periods, commas, colons, semicolons, parentheses.
- The renderer clones the agent-orchestrator web app; build UI from shadcn primitives (`components/ui/*`) and the Radix Dialog pattern already used by `NewTaskDialog.tsx`.
- App state under `~/.ao` only (not touched here).
- The existing resume path and the interactive dirty-refusal removal path stay behaviorally unchanged.
- Do not hand-edit generated sqlc/OpenAPI output. This feature adds no routes, so no regeneration is needed.
- Git author is already configured (dev@theharshitsingh.com); add `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>` to commits.

---

### Task 1: Typed error for un-resumable restore (fixes the 500)

**Files:**

- Modify: `backend/internal/session_manager/manager.go` (sentinel near line 25; the "nothing to resume from" return at line 480)
- Modify: `backend/internal/service/session/service.go` (`toAPIError`, near line 450)
- Test: `backend/internal/service/session/service_test.go` (new test for the mapping)

**Interfaces:**

- Produces: `sessionmanager.ErrNotResumable` (a sentinel `error`), and the wire contract `409` with code `SESSION_NOT_RESUMABLE` from `POST /api/v1/sessions/{id}/restore` when a terminated session has neither `agent_session_id` nor `prompt`. Task 2 (frontend) consumes the `SESSION_NOT_RESUMABLE` code.

- [ ] **Step 1: Write the failing test**

In `backend/internal/service/session/service_test.go`, add (mirror the package's existing test style and imports; `apierr` is `backend/internal/apierr`, `sessionmanager` is the session_manager package alias already used in `service.go`):

```go
func TestToAPIError_NotResumable(t *testing.T) {
	err := toAPIError(fmt.Errorf("restore foo: %w", sessionmanager.ErrNotResumable))
	var ae *apierr.Error
	if !errors.As(err, &ae) {
		t.Fatalf("want *apierr.Error, got %T: %v", err, err)
	}
	if ae.Kind != apierr.KindConflict {
		t.Errorf("kind = %v, want %v", ae.Kind, apierr.KindConflict)
	}
	if ae.Code != "SESSION_NOT_RESUMABLE" {
		t.Errorf("code = %q, want SESSION_NOT_RESUMABLE", ae.Code)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/service/session/ -run TestToAPIError_NotResumable`
Expected: FAIL to COMPILE with `undefined: sessionmanager.ErrNotResumable`.

- [ ] **Step 3: Add the sentinel**

In `backend/internal/session_manager/manager.go`, in the `var (...)` error block near line 25 (next to `ErrNotRestorable`, `ErrIncompleteHandle`), add:

```go
	// ErrNotResumable means a terminated session has no saved agent session id
	// and no prompt, so there is nothing for Restore to relaunch from. Distinct
	// from ErrNotRestorable (which is "not terminal yet").
	ErrNotResumable = errors.New("session: nothing to resume from")
```

- [ ] **Step 4: Return the sentinel from Restore**

In `backend/internal/session_manager/manager.go`, change the plain error at line 480 (inside `Restore`) from:

```go
	if meta.AgentSessionID == "" && meta.Prompt == "" {
		return domain.SessionRecord{}, fmt.Errorf("restore %s: nothing to resume from", id)
	}
```

to:

```go
	if meta.AgentSessionID == "" && meta.Prompt == "" {
		return domain.SessionRecord{}, fmt.Errorf("restore %s: %w", id, ErrNotResumable)
	}
```

- [ ] **Step 5: Map the sentinel in `toAPIError`**

In `backend/internal/service/session/service.go`, inside `toAPIError`, add a case alongside the sibling cases (after the `ErrIncompleteHandle` case, around line 455):

```go
	case errors.Is(err, sessionmanager.ErrNotResumable):
		return apierr.Conflict("SESSION_NOT_RESUMABLE",
			"This session has no saved agent session or prompt to resume from", nil)
```

- [ ] **Step 6: Run the test to verify it passes**

Run: `cd backend && go test ./internal/service/session/ -run TestToAPIError_NotResumable`
Expected: PASS.

- [ ] **Step 7: Build, vet, and run the touched packages**

Run: `cd backend && go build ./... && go vet ./internal/session_manager/... ./internal/service/session/... && go test ./internal/session_manager/... ./internal/service/session/...`
Expected: build clean, vet clean, all tests PASS (no behavior change to existing restore tests since the error value still wraps the same condition).

- [ ] **Step 8: Commit**

```bash
git add backend/internal/session_manager/manager.go backend/internal/service/session/service.go backend/internal/service/session/service_test.go
git commit -m "fix(session): return typed SESSION_NOT_RESUMABLE instead of 500 on un-resumable restore

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 2: Restore-unavailable popup + recreate via existing orchestrator endpoint

**Files:**

- Modify: `frontend/src/renderer/lib/spawn-orchestrator.ts` (optional `clean` param)
- Create: `frontend/src/renderer/components/RestoreUnavailableDialog.tsx` (the popup)
- Modify: `frontend/src/renderer/components/TerminalPane.tsx` (route `SESSION_NOT_RESUMABLE` to the dialog)
- Test: `frontend/src/renderer/lib/spawn-orchestrator.test.ts` (new; clean param)

**Interfaces:**

- Consumes from Task 1: the restore response error envelope `{ code: "SESSION_NOT_RESUMABLE", message, ... }`.
- Consumes existing: `spawnOrchestrator(projectId, clean?)` (extended here), `isOrchestrator(session)` from `frontend/src/renderer/types/workspace.ts`, `apiClient`/`apiErrorMessage` from `lib/api-client`, `workspaceQueryKey` already imported in `TerminalPane.tsx`.
- Produces: `RestoreUnavailableDialog` React component with props `{ open: boolean; session: SessionView; onOpenChange: (open: boolean) => void; onRecreated: (newOrchestratorId: string) => void }`.

- [ ] **Step 1: Write the failing test for the `clean` param**

Create `frontend/src/renderer/lib/spawn-orchestrator.test.ts` (mirror the mocking style in existing `frontend/src/renderer/**/*.test.ts(x)`; vitest is already configured):

```ts
import { describe, expect, it, vi, beforeEach } from "vitest";
import { spawnOrchestrator } from "./spawn-orchestrator";
import { apiClient } from "./api-client";

vi.mock("./api-client", () => ({
	apiClient: { POST: vi.fn() },
}));

describe("spawnOrchestrator", () => {
	beforeEach(() => vi.clearAllMocks());

	it("sends clean:true through to the request body when asked", async () => {
		(apiClient.POST as ReturnType<typeof vi.fn>).mockResolvedValue({
			data: { orchestrator: { id: "proj-9" } },
			error: undefined,
			response: { status: 201 },
		});
		const id = await spawnOrchestrator("proj", true);
		expect(id).toBe("proj-9");
		expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/orchestrators", {
			body: { projectId: "proj", clean: true },
		});
	});

	it("defaults clean to false / omitted for the existing call sites", async () => {
		(apiClient.POST as ReturnType<typeof vi.fn>).mockResolvedValue({
			data: { orchestrator: { id: "proj-1" } },
			error: undefined,
			response: { status: 201 },
		});
		await spawnOrchestrator("proj");
		expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/orchestrators", {
			body: { projectId: "proj", clean: false },
		});
	});
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd frontend && npx vitest run src/renderer/lib/spawn-orchestrator.test.ts`
Expected: FAIL (current helper sends `{ projectId }` with no `clean`).

- [ ] **Step 3: Add the `clean` param to the helper**

Edit `frontend/src/renderer/lib/spawn-orchestrator.ts`:

```ts
import { apiClient } from "./api-client";

/** Spawn the project's orchestrator session via the daemon API. When clean is
 *  true the daemon first tears down any active orchestrator for the project, then
 *  re-spawns one on the canonical branch (reattaching the existing branch). */
export async function spawnOrchestrator(projectId: string, clean = false): Promise<string> {
	const { data, error, response } = await apiClient.POST("/api/v1/orchestrators", {
		body: { projectId, clean },
	});

	if (error || !data?.orchestrator?.id) {
		const message =
			error && typeof error === "object" && "message" in error && typeof error.message === "string"
				? error.message
				: `Failed to spawn orchestrator (${response.status})`;
		throw new Error(message);
	}

	return data.orchestrator.id;
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd frontend && npx vitest run src/renderer/lib/spawn-orchestrator.test.ts`
Expected: PASS (both cases).

- [ ] **Step 5: Create the popup component**

Create `frontend/src/renderer/components/RestoreUnavailableDialog.tsx` (mirror the Radix Dialog structure/styling of `NewTaskDialog.tsx`; reuse `Button` from `./ui/button`):

```tsx
import * as Dialog from "@radix-ui/react-dialog";
import { Loader2 } from "lucide-react";
import { useState } from "react";
import { Button } from "./ui/button";
import { spawnOrchestrator } from "../lib/spawn-orchestrator";
import { isOrchestrator } from "../types/workspace";
import type { SessionView } from "../types/workspace";

type RestoreUnavailableDialogProps = {
	open: boolean;
	session: SessionView;
	onOpenChange: (open: boolean) => void;
	onRecreated: (newOrchestratorId: string) => void;
};

export function RestoreUnavailableDialog({ open, session, onOpenChange, onRecreated }: RestoreUnavailableDialogProps) {
	const [busy, setBusy] = useState(false);
	const [error, setError] = useState<string | undefined>();
	const orchestrator = isOrchestrator(session);

	const recreate = async () => {
		setBusy(true);
		setError(undefined);
		try {
			const id = await spawnOrchestrator(session.projectId, true);
			onOpenChange(false);
			onRecreated(id);
		} catch (err) {
			setError(err instanceof Error ? err.message : "Failed to create orchestrator");
		} finally {
			setBusy(false);
		}
	};

	return (
		<Dialog.Root open={open} onOpenChange={onOpenChange}>
			<Dialog.Portal>
				<Dialog.Overlay className="fixed inset-0 z-50 bg-black/50" />
				<Dialog.Content className="fixed left-1/2 top-1/2 z-50 w-[420px] -translate-x-1/2 -translate-y-1/2 rounded-lg border border-border bg-surface p-5 shadow-lg">
					<Dialog.Title className="text-sm font-medium text-foreground">Session can no longer be restored</Dialog.Title>
					<Dialog.Description className="mt-2 text-[13px] text-muted-foreground">
						{orchestrator
							? "This orchestrator has no saved agent session to resume. You can create a new orchestrator on the same branch; its committed work is preserved and the old worktree is cleaned."
							: "This session has no saved agent session or prompt to resume from."}
					</Dialog.Description>
					{error && <div className="mt-3 text-[12px] text-destructive">{error}</div>}
					<div className="mt-4 flex justify-end gap-2">
						<Button variant="ghost" onClick={() => onOpenChange(false)} disabled={busy}>
							{orchestrator ? "Cancel" : "Close"}
						</Button>
						{orchestrator && (
							<Button onClick={recreate} disabled={busy}>
								{busy && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
								Create new orchestrator
							</Button>
						)}
					</div>
				</Dialog.Content>
			</Dialog.Portal>
		</Dialog.Root>
	);
}
```

Note: confirm `Button` supports the `variant="ghost"` prop (check `./ui/button`); if its variant names differ, use the existing equivalent for a secondary/cancel button. Confirm `SessionView` exposes `projectId` and `kind`; if `projectId` is named differently on the view type, use the actual field.

- [ ] **Step 6: Wire the restore handler in `TerminalPane.tsx`**

In `frontend/src/renderer/components/TerminalPane.tsx`, add state and a dialog mount, and branch the restore error on the `SESSION_NOT_RESUMABLE` code. The existing handler is `restoreSession` (around lines 85-100) and the error is `restoreError` from `apiClient.POST(".../restore")`.

Add state near the other `useState` hooks in `AttachedTerminal`:

```tsx
const [restoreUnavailable, setRestoreUnavailable] = useState(false);
```

Replace the `catch`/error handling inside `restoreSession` so a `SESSION_NOT_RESUMABLE` code opens the dialog instead of setting the inline error. The `restoreError` returned by `apiClient.POST` is the parsed error envelope, so read its `code`:

```tsx
try {
	const { error: restoreError } = await apiClient.POST("/api/v1/sessions/{sessionId}/restore", {
		params: { path: { sessionId: session.id } },
	});
	if (restoreError) {
		const code = (restoreError as { code?: string }).code;
		if (code === "SESSION_NOT_RESUMABLE") {
			setRestoreUnavailable(true);
			return;
		}
		throw new Error(apiErrorMessage(restoreError, "Unable to restore session"));
	}
	await queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
} catch (err) {
	setRestoreError(err instanceof Error ? err.message : "Unable to restore session");
} finally {
	setIsRestoring(false);
}
```

Mount the dialog inside the component's returned JSX (e.g. just before the closing tag of the root `div` in `AttachedTerminal`, alongside the other absolutely-positioned children):

```tsx
{
	session && (
		<RestoreUnavailableDialog
			open={restoreUnavailable}
			session={session}
			onOpenChange={setRestoreUnavailable}
			onRecreated={async () => {
				await queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
			}}
		/>
	);
}
```

Add the import at the top of the file:

```tsx
import { RestoreUnavailableDialog } from "./RestoreUnavailableDialog";
```

Note: `onRecreated` here just refreshes the workspace so the new orchestrator appears in the list. If the renderer has an existing "select session" mechanism reachable from this component, call it with the new id; otherwise the invalidate is sufficient and the user picks the new orchestrator from the refreshed list. Do not invent a selection API that does not exist.

- [ ] **Step 7: Typecheck and run the frontend tests**

Run: `cd frontend && npx tsc --noEmit 2>&1 | grep -v "forge.config" ; npx vitest run src/renderer/lib/spawn-orchestrator.test.ts`
Expected: no NEW typecheck errors (only the pre-existing `forge.config.ts` `osxNotarize` error is acceptable); vitest PASS.

- [ ] **Step 8: Commit**

```bash
git add frontend/src/renderer/lib/spawn-orchestrator.ts frontend/src/renderer/lib/spawn-orchestrator.test.ts frontend/src/renderer/components/RestoreUnavailableDialog.tsx frontend/src/renderer/components/TerminalPane.tsx
git commit -m "feat(renderer): offer recreate-orchestrator popup when a session cannot be restored

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Manual verification (after both tasks, requires a rebuild of the packaged app)

1. `cd frontend && export NVM_DIR="$HOME/.nvm"; . "$NVM_DIR/nvm.sh"; nvm use 22.17.0; npm run make` (signed/notarized build per the release runbook) or run the dev app.
2. Terminate an orchestrator that has no saved agent session (e.g. a stale one), so the UI shows its "Restore session" button.
3. Click "Restore session". Expected: a popup appears (NOT "Internal server error"), titled "Session can no longer be restored".
4. Click "Create new orchestrator". Expected: a fresh orchestrator launches on the same `ao/<prefix>-orchestrator` branch with its committed history intact, and appears in the session list.
5. Confirm a worker that cannot be restored shows the same popup with a Close-only button (no recreate).

## Self-review notes

- Spec coverage: Task 1 covers the typed-error fix (spec Backend #1); Task 2 covers the popup + recreate via the existing `/orchestrators` endpoint (spec Backend #2 reuse + Frontend). The spec's "no new endpoint / no OpenAPI regen" is honored.
- Type consistency: `ErrNotResumable` (Task 1) is the symbol consumed by `toAPIError`; `SESSION_NOT_RESUMABLE` is the wire code consumed by Task 2's handler; `spawnOrchestrator(projectId, clean)` signature is defined in Task 2 Step 3 and consumed in Step 5.
- The two `Note:` callouts (Button variant name, SessionView `projectId` field, selection API) flag the only spots where the exact local name must be confirmed against the codebase during implementation; the implementer verifies rather than guessing.
