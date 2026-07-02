# Crash-proof Session Reconcile Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** On every daemon boot, reconcile live tmux + DB state so a SIGKILL/crash/force-quit that skips `SaveAndTeardownAll` no longer leaks an orphaned daemon, tmux sessions, or worktrees.

**Architecture:** Add `Manager.Reconcile(ctx)` to the session manager: a live pass (adopt alive sessions, stash+terminate dead ones), a reap pass (`Destroy` tmux of terminated sessions whose pane survived), then the existing `RestoreAll` body. Wire it in place of the bare `RestoreAll` call at daemon boot. On the frontend, add a kill+replace branch for a wedged orphan daemon on launch. tmux is the persistence layer, so adopting a crash-surviving session is a no-op.

**Tech Stack:** Go 1.x (backend, `go test`), TypeScript/Electron (frontend, `npm test` in `frontend/`). tmux runtime adapter. SQLite store.

## Global Constraints

- No em dashes (`—`) or en dashes (`–`) anywhere: prose, code comments, commit messages. Use a period, comma, colon, semicolon, or parentheses.
- Go: run `gofmt`/`goimports`; keep `golangci-lint` clean (the repo's CI gates on it).
- Git author email: `dev@theharshitsingh.com`. Commit with `git -c user.email=dev@theharshitsingh.com commit ...`.
- TDD: write the failing test first, watch it fail, implement minimally, watch it pass, commit.
- Reconcile is best-effort: per-session failures log and never abort the pass or block boot (same contract as the existing `RestoreAll` call site at `backend/internal/daemon/daemon.go:147`).
- Reconcile never deletes worktree directories and never spawns a new agent. Dirty worktrees are always preserved.

---

## File Structure

- `backend/internal/session_manager/manager.go` — add `Reconcile`, `reconcileLive`, `reconcileReap` methods; widen the `runtimeController` interface with `IsAlive`. The existing `RestoreAll` body is reused (called as the restore phase).
- `backend/internal/session_manager/manager_test.go` — add `IsAlive` to `fakeRuntime` (scriptable per handle); add `Reconcile` unit tests.
- `backend/internal/daemon/lifecycle_wiring.go` — add `Reconcile` to the `sessionLifecycle` interface.
- `backend/internal/daemon/daemon.go` — replace the `RestoreAll(ctx)` boot call with `Reconcile(ctx)`.
- `backend/internal/daemon/wiring_test.go` — update the `sessionLifecycle` fake/mock if it asserts the interface.
- `backend/internal/integration/lifecycle_sqlite_test.go` — add a reconcile integration case.
- `frontend/src/main.ts` — add the wedged-orphan kill+replace branch in `startDaemonInner`.
- `frontend/src/main.test.ts` (or the existing main-process test file) — test the kill+replace decision.

---

## Task 1: Widen `runtimeController` with `IsAlive` and adopt-alive live pass

**Files:**

- Modify: `backend/internal/session_manager/manager.go:64-67` (interface), add methods near `manager.go:558-623`
- Test: `backend/internal/session_manager/manager_test.go:138-152` (fake), new test fn

**Interfaces:**

- Consumes: `domain.SessionRecord` (`.IsTerminated`, `.Metadata.WorkspacePath`, `.Metadata.Branch`, `.Metadata.RuntimeHandleID`); `runtimeHandle(meta)` -> `ports.RuntimeHandle`; `workspaceInfo(rec)` -> `ports.WorkspaceInfo`; `m.workspace.StashUncommitted`, `m.lcm.MarkTerminated`, `m.store.ListAllSessions`.
- Produces: `func (m *Manager) reconcileLive(ctx context.Context, rec domain.SessionRecord) error`; widened `runtimeController` with `IsAlive(ctx context.Context, handle ports.RuntimeHandle) (bool, error)`.

- [ ] **Step 1: Add `IsAlive` to the test `fakeRuntime`**

In `manager_test.go`, extend the fake so `IsAlive` is scriptable per handle id and records calls:

```go
type fakeRuntime struct {
	createErr          error
	created, destroyed int
	lastCfg            ports.RuntimeConfig
	// aliveByHandle maps a RuntimeHandle.ID to its liveness; missing = false.
	aliveByHandle map[string]bool
	aliveErr      error
	destroyedIDs  []string
}

func (r *fakeRuntime) IsAlive(_ context.Context, handle ports.RuntimeHandle) (bool, error) {
	if r.aliveErr != nil {
		return false, r.aliveErr
	}
	return r.aliveByHandle[handle.ID], nil
}
```

Also record the destroyed handle id in the existing `Destroy`:

```go
func (r *fakeRuntime) Destroy(_ context.Context, handle ports.RuntimeHandle) error {
	r.destroyed++
	r.destroyedIDs = append(r.destroyedIDs, handle.ID)
	return nil
}
```

- [ ] **Step 2: Write the failing test for the live pass**

Add to `manager_test.go`. A live (`is_terminated=0`) session whose tmux is GONE must be stashed and marked terminated; an ALIVE one must be left untouched.

```go
func TestReconcileLive_DeadSessionStashedAndTerminated(t *testing.T) {
	st := newFakeStore()
	rt := &fakeRuntime{aliveByHandle: map[string]bool{}} // handle not alive
	ws := &fakeWorkspace{stashRef: "refs/ao/preserved/s1"}
	lcm := &fakeLCM{}
	m := newManager(t, st, rt, ws, lcm)

	rec := domain.SessionRecord{
		ID:          "s1",
		ProjectID:   "p1",
		IsTerminated: false,
		Metadata: domain.SessionMetadata{
			Branch: "ao/s1/root", WorkspacePath: "/wt/s1", RuntimeHandleID: "s1",
		},
	}

	if err := m.reconcileLive(context.Background(), rec); err != nil {
		t.Fatalf("reconcileLive: %v", err)
	}
	if ws.stashCalls != 1 {
		t.Fatalf("StashUncommitted calls = %d, want 1", ws.stashCalls)
	}
	if lcm.terminated["s1"] != 1 {
		t.Fatalf("MarkTerminated(s1) = %d, want 1", lcm.terminated["s1"])
	}
	if rt.destroyed != 0 {
		t.Fatalf("Destroy calls = %d, want 0 (dead session: no tmux to kill)", rt.destroyed)
	}
}

func TestReconcileLive_AliveSessionAdoptedNoop(t *testing.T) {
	st := newFakeStore()
	rt := &fakeRuntime{aliveByHandle: map[string]bool{"s2": true}}
	ws := &fakeWorkspace{}
	lcm := &fakeLCM{}
	m := newManager(t, st, rt, ws, lcm)

	rec := domain.SessionRecord{
		ID: "s2", ProjectID: "p1", IsTerminated: false,
		Metadata: domain.SessionMetadata{Branch: "ao/s2/root", WorkspacePath: "/wt/s2", RuntimeHandleID: "s2"},
	}

	if err := m.reconcileLive(context.Background(), rec); err != nil {
		t.Fatalf("reconcileLive: %v", err)
	}
	if ws.stashCalls != 0 || lcm.terminated["s2"] != 0 || rt.destroyed != 0 {
		t.Fatalf("adopt should be a no-op: stash=%d term=%d destroy=%d", ws.stashCalls, lcm.terminated["s2"], rt.destroyed)
	}
}
```

> If `fakeWorkspace` lacks a `stashCalls` counter or `fakeLCM` lacks a `terminated` map, add them: increment `stashCalls` inside `fakeWorkspace.StashUncommitted`, and `l.terminated[id]++` (init the map in the fake) inside `fakeLCM.MarkTerminated`. If `newManager` has a different signature in this file, match the existing constructor used by other tests rather than inventing one.

- [ ] **Step 3: Run the test, verify it fails**

Run: `cd backend && go test ./internal/session_manager/ -run TestReconcileLive -v`
Expected: FAIL — `m.reconcileLive` undefined, and `IsAlive` not in `runtimeController`.

- [ ] **Step 4: Widen the interface and implement `reconcileLive`**

In `manager.go`, widen the interface (around line 64):

```go
type runtimeController interface {
	Create(ctx context.Context, cfg ports.RuntimeConfig) (ports.RuntimeHandle, error)
	Destroy(ctx context.Context, handle ports.RuntimeHandle) error
	// IsAlive reports whether the handle's runtime session still exists. Used by
	// Reconcile on boot to adopt crash-surviving sessions and reap leaked ones.
	IsAlive(ctx context.Context, handle ports.RuntimeHandle) (bool, error)
}
```

Add the method (place it near `saveAndTeardownOne`, around line 623):

```go
// reconcileLive handles a single non-terminated session on boot. If its runtime
// session is still alive (tmux is the persistence layer, so it survives a daemon
// crash) we adopt it: a no-op, the agent keeps running. If the runtime is gone,
// the agent died with the daemon, so we capture any uncommitted work into a
// preserve ref (best-effort) and mark the session terminated. We never relaunch
// here (that is spawn policy) and never delete the worktree.
func (m *Manager) reconcileLive(ctx context.Context, rec domain.SessionRecord) error {
	if rec.Metadata.WorkspacePath == "" || rec.Metadata.Branch == "" {
		return nil
	}
	handle := runtimeHandle(rec.Metadata)
	if handle.ID != "" {
		alive, err := m.runtime.IsAlive(ctx, handle)
		if err != nil {
			// A failed probe is not proof of death: leave the session as-is.
			return fmt.Errorf("reconcile %s: probe: %w", rec.ID, err)
		}
		if alive {
			return nil // adopt: the session survived the crash.
		}
	}
	// Runtime is gone: preserve work (best-effort) then mark terminated.
	if _, err := m.workspace.StashUncommitted(ctx, workspaceInfo(rec)); err != nil {
		m.logger.Warn("reconcile: stash uncommitted failed; marking terminated anyway", "sessionID", rec.ID, "error", err)
	}
	if err := m.lcm.MarkTerminated(ctx, rec.ID); err != nil {
		return fmt.Errorf("reconcile %s: mark terminated: %w", rec.ID, err)
	}
	return nil
}
```

- [ ] **Step 5: Run the tests, verify they pass**

Run: `cd backend && go test ./internal/session_manager/ -run TestReconcileLive -v`
Expected: PASS (both cases).

- [ ] **Step 6: Build to confirm the widened interface still satisfies the concrete runtime**

Run: `cd backend && go build ./...`
Expected: success (the concrete `runtimeselect.Runtime`/`tmux.Runtime` already implement `IsAlive`).

- [ ] **Step 7: Commit**

```bash
cd backend && gofmt -w internal/session_manager/manager.go internal/session_manager/manager_test.go
git add internal/session_manager/manager.go internal/session_manager/manager_test.go
git -c user.email=dev@theharshitsingh.com commit -m "feat(session): reconcile live pass (adopt alive, stash+terminate dead)"
```

---

## Task 2: Reap pass and the `Reconcile` entry point

**Files:**

- Modify: `backend/internal/session_manager/manager.go` (add `reconcileReap`, `Reconcile`; the latter reuses the existing `RestoreAll` body)
- Test: `backend/internal/session_manager/manager_test.go`

**Interfaces:**

- Consumes: `m.store.ListAllSessions`, `m.runtime.IsAlive`, `m.runtime.Destroy`, `reconcileLive` (Task 1), the existing `RestoreAll` method (`manager.go:637`).
- Produces: `func (m *Manager) Reconcile(ctx context.Context) error`; `func (m *Manager) reconcileReap(ctx context.Context, rec domain.SessionRecord) error`.

- [ ] **Step 1: Write the failing reap test**

Add to `manager_test.go`. A terminated session whose tmux is still alive must have its tmux `Destroy`d; a terminated session whose tmux is gone must not.

```go
func TestReconcileReap_TerminatedButAliveTmuxDestroyed(t *testing.T) {
	st := newFakeStore()
	rt := &fakeRuntime{aliveByHandle: map[string]bool{"t1": true}}
	ws := &fakeWorkspace{}
	lcm := &fakeLCM{}
	m := newManager(t, st, rt, ws, lcm)

	rec := domain.SessionRecord{
		ID: "t1", ProjectID: "p1", IsTerminated: true,
		Metadata: domain.SessionMetadata{RuntimeHandleID: "t1"},
	}

	if err := m.reconcileReap(context.Background(), rec); err != nil {
		t.Fatalf("reconcileReap: %v", err)
	}
	if len(rt.destroyedIDs) != 1 || rt.destroyedIDs[0] != "t1" {
		t.Fatalf("destroyedIDs = %v, want [t1]", rt.destroyedIDs)
	}
}

func TestReconcileReap_TerminatedAndDeadTmuxLeftAlone(t *testing.T) {
	st := newFakeStore()
	rt := &fakeRuntime{aliveByHandle: map[string]bool{}} // t2 not alive
	m := newManager(t, st, rt, &fakeWorkspace{}, &fakeLCM{})

	rec := domain.SessionRecord{
		ID: "t2", ProjectID: "p1", IsTerminated: true,
		Metadata: domain.SessionMetadata{RuntimeHandleID: "t2"},
	}
	if err := m.reconcileReap(context.Background(), rec); err != nil {
		t.Fatalf("reconcileReap: %v", err)
	}
	if rt.destroyed != 0 {
		t.Fatalf("Destroy calls = %d, want 0", rt.destroyed)
	}
}
```

- [ ] **Step 2: Run the test, verify it fails**

Run: `cd backend && go test ./internal/session_manager/ -run TestReconcileReap -v`
Expected: FAIL — `m.reconcileReap` undefined.

- [ ] **Step 3: Implement `reconcileReap` and `Reconcile`**

Add `reconcileReap` near `reconcileLive`:

```go
// reconcileReap kills the leaked tmux session of a session the DB already marks
// terminated. This covers the teardown that marked the row terminated but failed
// to kill the runtime (e.g. ForceDestroy/Destroy errored after MarkTerminated).
// Destroy is idempotent, so an already-gone session is a no-op.
func (m *Manager) reconcileReap(ctx context.Context, rec domain.SessionRecord) error {
	handle := runtimeHandle(rec.Metadata)
	if handle.ID == "" {
		return nil
	}
	alive, err := m.runtime.IsAlive(ctx, handle)
	if err != nil {
		return fmt.Errorf("reconcile reap %s: probe: %w", rec.ID, err)
	}
	if !alive {
		return nil
	}
	if err := m.runtime.Destroy(ctx, handle); err != nil {
		return fmt.Errorf("reconcile reap %s: destroy: %w", rec.ID, err)
	}
	return nil
}
```

Add the entry point. Place it just above `RestoreAll` (manager.go:625) and have it call the existing `RestoreAll` as the restore phase:

```go
// Reconcile is the boot-time consistency pass. It replaces the bare RestoreAll
// call so that however the previous daemon died (clean shutdown, SIGKILL, or
// crash), live reality matches the DB:
//
//  1. Live pass: for each non-terminated session, adopt it if its runtime
//     survived, else capture work and mark terminated (reconcileLive).
//  2. Reap pass: for each terminated session whose runtime leaked, kill it
//     (reconcileReap). Runs before restore so a restored session does not
//     collide with a leaked tmux of the same name.
//  3. Restore pass: relaunch shutdown-saved sessions (existing RestoreAll).
//
// Best-effort throughout: a per-session failure is logged and never aborts the
// pass or blocks boot.
func (m *Manager) Reconcile(ctx context.Context) error {
	recs, err := m.store.ListAllSessions(ctx)
	if err != nil {
		return fmt.Errorf("reconcile: list sessions: %w", err)
	}
	for _, rec := range recs {
		if rec.IsTerminated {
			continue
		}
		if err := m.reconcileLive(ctx, rec); err != nil {
			m.logger.Error("reconcile: live pass failed, skipping", "sessionID", rec.ID, "error", err)
		}
	}
	for _, rec := range recs {
		if !rec.IsTerminated {
			continue
		}
		if err := m.reconcileReap(ctx, rec); err != nil {
			m.logger.Error("reconcile: reap pass failed, skipping", "sessionID", rec.ID, "error", err)
		}
	}
	return m.RestoreAll(ctx)
}
```

> Note: the live pass re-reads `rec.IsTerminated` from the pre-pass snapshot, so a session terminated _by_ the live pass is not also reaped in the same run. That is fine: its tmux is already gone (that is why it was terminated), so reaping would be a no-op anyway.

- [ ] **Step 4: Run the tests, verify they pass**

Run: `cd backend && go test ./internal/session_manager/ -run 'TestReconcile' -v`
Expected: PASS (live + reap tests).

- [ ] **Step 5: Commit**

```bash
cd backend && gofmt -w internal/session_manager/manager.go internal/session_manager/manager_test.go
git add internal/session_manager/manager.go internal/session_manager/manager_test.go
git -c user.email=dev@theharshitsingh.com commit -m "feat(session): reconcile reap pass and Reconcile entry point"
```

---

## Task 3: Wire `Reconcile` into daemon boot

**Files:**

- Modify: `backend/internal/daemon/lifecycle_wiring.go:64-67` (interface)
- Modify: `backend/internal/daemon/daemon.go:144-149` (boot call)
- Test: `backend/internal/daemon/wiring_test.go`

**Interfaces:**

- Consumes: `Manager.Reconcile` (Task 2).
- Produces: `sessionLifecycle` interface gains `Reconcile(ctx context.Context) error`.

- [ ] **Step 1: Update the wiring test/mock**

In `wiring_test.go`, find the type used as a `sessionLifecycle` test double (it implements `RestoreAll` and `SaveAndTeardownAll`). Add a `Reconcile` method and, if the test asserts boot behavior, assert `Reconcile` is the method called on boot:

```go
func (m *fakeSessionLifecycle) Reconcile(ctx context.Context) error {
	m.reconcileCalls++
	return m.reconcileErr
}
```

(If `wiring_test.go` has no such double and only checks construction, add a compile-time assertion instead: `var _ sessionLifecycle = (*sessionmanager.Manager)(nil)` in the test, which fails to compile until both the interface and the concrete method exist.)

- [ ] **Step 2: Run the test, verify it fails**

Run: `cd backend && go test ./internal/daemon/ -run Wiring -v`
Expected: FAIL — `Reconcile` not in the interface / not asserted.

- [ ] **Step 3: Add `Reconcile` to the interface**

In `lifecycle_wiring.go`:

```go
type sessionLifecycle interface {
	Reconcile(ctx context.Context) error
	RestoreAll(ctx context.Context) error
	SaveAndTeardownAll(ctx context.Context) error
}
```

- [ ] **Step 4: Replace the boot call**

In `daemon.go`, change the boot restore call (currently lines 144-149) to call `Reconcile`:

```go
	// Reconcile sessions on boot: adopt crash-surviving runtimes, capture and
	// terminate dead ones, reap leaked tmux, then restore shutdown-saved
	// sessions. Best-effort: a failure is logged but never blocks boot. Placed
	// before srv.Run so sessions are consistent before the server serves.
	if reconcileErr := sessMgr.Reconcile(ctx); reconcileErr != nil {
		log.Error("reconcile sessions on boot failed", "err", reconcileErr)
	}
```

- [ ] **Step 5: Run the tests, verify they pass**

Run: `cd backend && go test ./internal/daemon/ -v`
Expected: PASS.

- [ ] **Step 6: Build everything**

Run: `cd backend && go build ./... && go vet ./internal/daemon/ ./internal/session_manager/`
Expected: success.

- [ ] **Step 7: Commit**

```bash
cd backend && gofmt -w internal/daemon/daemon.go internal/daemon/lifecycle_wiring.go internal/daemon/wiring_test.go
git add internal/daemon/daemon.go internal/daemon/lifecycle_wiring.go internal/daemon/wiring_test.go
git -c user.email=dev@theharshitsingh.com commit -m "feat(daemon): run Reconcile on boot in place of bare RestoreAll"
```

---

## Task 4: Integration test over the sqlite store

**Files:**

- Modify: `backend/internal/integration/lifecycle_sqlite_test.go`

**Interfaces:**

- Consumes: the real `Manager.Reconcile`, a real sqlite store, and the test's runtime fake (find how this file already fakes the runtime; reuse it, scripting `IsAlive` per handle).

- [ ] **Step 1: Read the existing integration harness**

Open `backend/internal/integration/lifecycle_sqlite_test.go`. Identify how it constructs a `Manager` with a real `sqlite.Store` and what runtime double it injects. Reuse that exact wiring; only add `IsAlive` scripting to the double if it is missing.

- [ ] **Step 2: Write the failing integration test**

Add a test that seeds two sessions through the store, runs `Reconcile`, and asserts the resulting DB state. Use the file's existing seeding helpers and constructor names (match them; do not invent new ones):

```go
func TestReconcile_TerminatesDeadLiveSessionAndReapsLeakedTmux(t *testing.T) {
	// ... build store + manager the same way other tests in this file do ...

	// Seed A: is_terminated=0 but its runtime is gone (crash-killed agent).
	// Seed B: is_terminated=1 but its tmux is still alive (leaked teardown).
	// Script the runtime double: A's handle -> not alive, B's handle -> alive.

	if err := mgr.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	// A is now terminated in the store.
	a, _, _ := store.GetSession(ctx, "A")
	if !a.IsTerminated {
		t.Fatalf("session A: want terminated after reconcile")
	}
	// B's leaked tmux was destroyed.
	if !runtimeDouble.wasDestroyed("B") {
		t.Fatalf("session B: want leaked tmux destroyed")
	}
}
```

> Replace `mgr`, `store`, `ctx`, `runtimeDouble`, and the seeding with the file's actual identifiers. If the file's runtime double cannot report `wasDestroyed`, assert via the store/observable side effects it already uses.

- [ ] **Step 3: Run it, verify it fails**

Run: `cd backend && go test ./internal/integration/ -run TestReconcile_TerminatesDeadLiveSessionAndReapsLeakedTmux -v`
Expected: FAIL (until seeding + assertions match real helpers; iterate until it compiles and fails for the right reason, then passes once Reconcile runs).

- [ ] **Step 4: Make it pass**

The production code from Tasks 1-3 already implements the behavior. Adjust only the test scaffolding (identifiers, seeding) until it passes.

Run: `cd backend && go test ./internal/integration/ -run TestReconcile -v`
Expected: PASS.

- [ ] **Step 5: Run the full backend suite**

Run: `cd backend && go test ./...`
Expected: PASS (no regressions in session_manager, daemon, integration).

- [ ] **Step 6: Commit**

```bash
cd backend && gofmt -w internal/integration/lifecycle_sqlite_test.go
git add internal/integration/lifecycle_sqlite_test.go
git -c user.email=dev@theharshitsingh.com commit -m "test(integration): reconcile terminates dead-live sessions and reaps leaked tmux"
```

---

## Task 5: Frontend wedged-orphan kill+replace branch

**Files:**

- Modify: `frontend/src/main.ts` (in `startDaemonInner`, around lines 457-495)
- Test: `frontend/src/main.test.ts` or the existing main-process test file

**Interfaces:**

- Consumes: existing `inspectExistingDaemon`, `resolveDaemonFromPort`, `readDaemonProbe`, `killDaemon`, `parseRunFile`/`defaultRunFilePath`, `expectedDaemonPort`.
- Produces: a pure decision helper, e.g. `function planDaemonTakeover(probe: DaemonProbe | null): "reuse" | "replace"`, unit-testable without spawning.

- [ ] **Step 1: Read the current launch flow**

Read `frontend/src/main.ts:432-512`. Confirm: `inspectExistingDaemon` returns a status when the run-file agrees with a live daemon; `resolveDaemonFromPort` attaches when a daemon answers the port. The gap: when a process holds the port but is unhealthy (no `/healthz` + `/readyz`) or identity-mismatched, today the code falls through to `spawn`, and the Go child then refuses the port and exits 1. We add: detect that case and kill the holder first.

- [ ] **Step 2: Write the failing unit test for the decision helper**

In the frontend test file:

```ts
import { planDaemonTakeover } from "./main";

test("healthy probe -> reuse", () => {
	expect(planDaemonTakeover({ healthy: true, pid: 123, port: 3001 })).toBe("reuse");
});

test("port held but unhealthy probe -> replace", () => {
	expect(planDaemonTakeover({ healthy: false, pid: 123, port: 3001 })).toBe("replace");
});

test("no probe (nothing on port) -> replace (spawn fresh)", () => {
	expect(planDaemonTakeover(null)).toBe("replace");
});
```

> Match `DaemonProbe`'s real shape from `frontend/src/shared/` (the `readDaemonProbe` return type). If it exposes health via a different field (e.g. presence of both `healthz` and `readyz`), encode that in `planDaemonTakeover` and the test rather than a `healthy` boolean.

- [ ] **Step 3: Run it, verify it fails**

Run: `cd frontend && npm test -- planDaemonTakeover`
Expected: FAIL — `planDaemonTakeover` not exported.

- [ ] **Step 4: Implement the helper and wire the branch**

Add the pure helper (top-level, exported) in `main.ts`:

```ts
// planDaemonTakeover decides what to do with whatever currently holds the daemon
// port on launch. A healthy daemon is reused (it kept sessions alive across a
// crash). Anything else - an unhealthy/wedged holder, or nothing answering - means
// spawn fresh; the caller kills a live-but-unhealthy holder first.
export function planDaemonTakeover(probe: DaemonProbe | null): "reuse" | "replace" {
	return probe?.healthy ? "reuse" : "replace";
}
```

Then, in `startDaemonInner`, after the existing `inspectExistingDaemon` + `resolveDaemonFromPort` attach attempts fail (i.e. just before `spawn`), add: probe the expected port; if something answers but is unhealthy, SIGTERM the holder via the run-file PID and wait for the port to free before spawning. Concretely, before the `spawn(...)` at line 505:

```ts
// A process may hold the port without being a healthy daemon we can attach to
// (wedged orphan from a crash, or a PID-dead-but-port-held run-file). Spawning
// then would make the Go child collide and exit 1. Detect it and clear it.
const holderProbe = await readDaemonProbe(expectedDaemonPort(process.env));
if (planDaemonTakeover(holderProbe) === "replace" && holderProbe) {
	const runFile = parseRunFile(await readRunFileSafe(defaultRunFilePath()));
	if (runFile?.pid) {
		try {
			process.kill(-runFile.pid, "SIGTERM");
		} catch {
			try {
				process.kill(runFile.pid, "SIGTERM");
			} catch {
				/* already gone */
			}
		}
	}
	await waitForPortFree(expectedDaemonPort(process.env), 8_000);
	await rmRunFileSafe(defaultRunFilePath());
}
```

> Use the file's existing run-file read/parse helpers (`parseRunFile`, `defaultRunFilePath`). If `readRunFileSafe`/`rmRunFileSafe`/`waitForPortFree` do not exist, add small local helpers: `readRunFileSafe` wraps `fs.readFile` returning `""` on ENOENT; `rmRunFileSafe` wraps `fs.rm` ignoring ENOENT; `waitForPortFree` polls `readDaemonProbe` until it returns null or the timeout elapses. Keep each to a few lines, matching the file's existing async style.

- [ ] **Step 5: Run the tests, verify they pass**

Run: `cd frontend && npm test -- planDaemonTakeover`
Expected: PASS.

- [ ] **Step 6: Type-check and lint the frontend**

Run: `cd frontend && npm run typecheck && npm run lint`
Expected: success (commands per `frontend/package.json`; if names differ, use the repo's configured equivalents).

- [ ] **Step 7: Commit**

```bash
cd frontend
git add src/main.ts src/main.test.ts
git -c user.email=dev@theharshitsingh.com commit -m "feat(frontend): kill+replace a wedged orphan daemon on launch"
```

---

## Task 6: Full verification and branch wrap-up

- [ ] **Step 1: Backend suite + lint**

Run: `cd backend && go test ./... && gofmt -l . && go vet ./...`
Expected: tests PASS, `gofmt -l` prints nothing. If `golangci-lint` is installed: `golangci-lint run ./internal/session_manager/... ./internal/daemon/...` clean.

- [ ] **Step 2: Frontend suite**

Run: `cd frontend && npm test`
Expected: PASS.

- [ ] **Step 3: Manual smoke (optional, real hardware)**

With the app/daemon running and at least one session live, `kill -9` the daemon PID (from `~/.ao/running.json`), then relaunch. Expect: the live session's tmux is adopted (still listed, agent intact), no duplicate daemon, `running.json` repointed to the new PID. Then kill a session's agent and `kill -9` the daemon: expect that session marked terminated with its work in a `refs/ao/preserved/<id>` ref, and no leaked tmux.

- [ ] **Step 4: Review the diff against the spec**

Confirm every spec section maps to a task: live pass (T1), reap + entry point (T2), boot wiring (T3), integration (T4), frontend takeover (T5). Confirm no worktree directory is ever deleted by reconcile and no agent is relaunched outside the existing `RestoreAll`.

- [ ] **Step 5: Push the branch**

```bash
git push -u origin feat/crash-proof-session-reconcile
```

---

## Self-Review notes (planning)

- **Spec coverage:** Component 1 (live + reap matrix) -> Tasks 1-2; Component 2 (order of phases) -> Task 2 `Reconcile`; Component 3 (frontend kill+replace) -> Task 5; error-handling contract -> best-effort logging in every pass; testing section -> Tasks 1, 2, 4, 5. Deferred `ListSessions` is explicitly not implemented (matches spec Deferred).
- **Type consistency:** `IsAlive(ctx, ports.RuntimeHandle) (bool, error)` matches the concrete tmux/conpty signature (`tmux.go:176`). `Reconcile(ctx) error`, `reconcileLive(ctx, domain.SessionRecord) error`, `reconcileReap(ctx, domain.SessionRecord) error` are used identically across tasks. `runtimeHandle`/`workspaceInfo` helpers exist at `manager.go:1135,1139`.
- **Placeholder scan:** test bodies that depend on existing fakes' field names (`stashCalls`, `terminated`, `newManager`) carry an explicit instruction to match the file's real identifiers; this is unavoidable without the fakes in front of the implementer and is called out at each use, not left as a silent TODO.
