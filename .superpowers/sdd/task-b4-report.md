# Task B4 Report: conpty Runtime Adapter

## Status: DONE

## Files Created

All under `backend/internal/adapters/runtime/conpty/`:

- `client.go` - loopback TCP client helpers (dialHost, clientSendMessage, clientGetOutput, clientIsAlive, clientKill)
- `spawn.go` - hostSpawner type definition
- `spawn_other.go` (`//go:build !windows`) - stub returning "conpty spawn: unsupported on this OS"
- `spawn_windows.go` (`//go:build windows`) - real detached-process spawner using `DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP` and `HideWindow: true` via `golang.org/x/sys/windows`
- `pidalive_unix.go` (`//go:build !windows`) - `pidAlive` (signal-0 probe) + `defaultOSProcessFinder`
- `pidalive_windows.go` (`//go:build windows`) - `pidAlive` (OpenProcess/SYNCHRONIZE probe) + `defaultOSProcessFinder`
- `runtime.go` - `Runtime` struct, `New`, `Create`, `Destroy`, `IsAlive`, `SendMessage`, `GetOutput`, `resolve`, and `var _ ports.Runtime = (*Runtime)(nil)` compile-time assertion
- `runtime_test.go` - full test suite (13 test functions)

## Session-Resolution + Spawn-Seam Design

**Spawn seam:** `Options.Spawner` is a `hostSpawner` func. Production code uses `defaultSpawnHost` (Windows-only real spawn). Tests inject a fake spawner that starts an in-process `Serve` + `fakePTY` on a real `127.0.0.1:0` listener and returns that addr plus a fake PID. This makes all adapter methods testable on Darwin without a real ConPTY.

**Session resolution:** `Runtime.resolve(id)` checks the in-memory `map[string]*hostSession` first (populated by `Create`). On a cache miss it calls `ptyregistry.List()` and scans for a matching `SessionID`, re-populating the map if found. This gives daemon-restart recovery: a pty-host spawned before a daemon restart is still reachable as long as its registry entry survives and its PID is alive.

**Registry storage:** The loopback addr (e.g. "127.0.0.1:54321") is stored in `Entry.PipePath` (reusing the existing field whose semantic is "how to reach the host"). The pty-host OS PID is stored in `Entry.PtyHostPID` as specified.

## Registry-Recovery Path

`Create` calls `ptyregistry.Register` with the loopback addr in `PipePath` and the pty-host PID in `PtyHostPID`. `Destroy` calls `ptyregistry.Unregister`. `resolve` calls `ptyregistry.List()` (which auto-prunes dead-PID entries) and returns a `hostSession` if found. The `TestResolveViaRegistry` test verifies this: a `Runtime` with an empty in-memory map resolves `IsAlive` and `SendMessage` via the registry entry alone.

**PID gotcha for tests:** `ptyregistry.List()` prunes entries whose `PtyHostPID` is not alive. Tests that need registry entries to survive the prune use `livePID()` (= `os.Getpid()`, always alive). The `Destroy` test uses `deadPID()` (= 2147483647, MaxInt32, never a real process) so the force-kill step is a safe no-op that does not accidentally kill the test runner.

## Verification Command Outputs

```
go build ./...                            SUCCESS
GOOS=windows go build ./...               SUCCESS
GOOS=linux go build ./...                 SUCCESS
go test -race ./internal/adapters/runtime/conpty/...   48 tests PASS (2 packages)
go vet ./internal/adapters/runtime/conpty/...          No issues
```

## Windows-Only Spawn File (spawn_windows.go)

`spawn_windows.go` was not executed (Darwin machine). The file compiles under `GOOS=windows go build ./...`. Key implementation notes:

- Uses `golang.org/x/sys/windows.SysProcAttr.CreationFlags = DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP` and `HideWindow: true`. This mirrors `detached: true, windowsHide: true` from the TS spawn.
- Reads stdout with a `bufio.Scanner` looking for `READY:<pid> <port>` (the format printed by `RunHost` in `host_main.go`).
- On success, calls `stdout.Close()` and `cmd.Process.Release()` to detach the child. `cmd.Process.Release()` is a best-effort detach; the error is intentionally ignored.
- 10s timeout matches the TS source (`10_000` ms).
- The env merge (inherit parent + overlay caller-provided vars) mirrors `{ ...process.env, ...config.environment }` from the TS.

Concern: `cmd.Process.Release()` on Windows after `cmd.Start()` does not prevent the child from being reaped if our process exits; it only releases the OS handle held by the Go runtime. The child's `DETACHED_PROCESS` flag is what truly detaches it from our console group. This is correct behavior.

## Interface Compliance

`var _ ports.Runtime = (*Runtime)(nil)` compiles, confirming `Create`, `Destroy`, and `IsAlive` are all implemented with the correct signatures. `SendMessage` and `GetOutput` are exported but not part of the `ports.Runtime` interface - they are called by the daemon's messenger and output layers directly. `Attach` is intentionally omitted (B5).

---

## Review fix: IsAlive reaper-safety (dead-vs-transient split)

### The bug
`clientIsAlive` returned a bare `bool`, collapsing dial timeout, read-deadline
expiry, write error, and connection-refused all to `false`. The reaper turns
`(false, nil)` into `ProbeDead`, which the LCM can promote to a permanent
`IsTerminated` reap when the agent has also been quiet >60s. A single transient
2s loopback timeout on a normal idle session would then spuriously kill a live
session. tmux/zellij avoid this by returning a non-nil error for transient
failures (recorded as `ProbeFailed`, ignored and retried).

### Fix (files touched, all under conpty/)
- `client.go`: `clientIsAlive` now returns `(alive bool, transientErr error)`:
  - valid `MSG_STATUS_RES` -> `(true, nil)`.
  - dial refused (nothing listening) -> `(false, nil)` definitively gone.
  - dial timeout / read-deadline expiry / write error / mid-read EOF / no
    STATUS_RES before conn end -> `(false, err)` transient.
  - Added `isTimeout` (net.Error.Timeout()) and `isConnRefused`
    (errors.Is ECONNREFUSED, plus WSAECONNREFUSED 10061 for older Windows).
    "When unsure, prefer transient": any non-timeout, non-refused dial error
    returns the error.
  - `clientGetOutput` "return empty on failure" behavior left unchanged.
- `runtime.go`: `IsAlive` now propagates `clientIsAlive`'s `(bool, error)`
  directly; the unknown-session path still returns `(false, nil)`.
- `runtime_test.go`:
  - Updated `TestClientIsAlive_TrueAndFalse` for the new 2-value signature
    (refused -> (false, nil)).
  - Added regression `TestIsAlive_RefusedIsGone_TimeoutIsTransient`:
    (a) resolved-but-refused host -> `(false, nil)`;
    (b) resolved host behind a listener that Accepts but never replies, so the
    short `isAliveTimeout` read deadline fires -> `(false, non-nil err)`.

### Command outputs (from backend/)
- `go build ./...`            : Success
- `GOOS=windows go build ./...`: Success
- `GOOS=linux go build ./...`  : Success
- `go test -race ./internal/adapters/runtime/conpty/...` : 49 passed in 2 packages
- `go vet ./internal/adapters/runtime/conpty/...`        : No issues found
- New test verbose: TestIsAlive_RefusedIsGone_TimeoutIsTransient PASS (2.00s)

### READY-format confirmation (host_main.go, not modified)
`host_main.go:54` prints `fmt.Fprintf(stdout, "READY:%d %d\n", pty.PID(), port)`
i.e. `READY:<pid> <port>` (pid THEN port), which matches spawn_windows.go's
`READY:(\d+) (\d+)` regex. No action needed (and out of scope for this task).
