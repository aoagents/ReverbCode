# Task B3 Report: pty-host serve engine + ConPTY seam

## Files created

All under `backend/internal/adapters/runtime/conpty/`:

| File | Role |
|---|---|
| `host.go` | Cross-platform serve engine: `ptyConn` interface, `ServeConfig`, `Serve()`, `host` event loop, `pumpPTY`, `broadcast`, `handleConn`, `handleClientMsg`, `statusFrame` |
| `host_conpty_windows.go` | `//go:build windows` real impl: `conptyConn` wrapping `github.com/aymanbagabas/go-pty` ConPty + Cmd, goroutine that Waits the process and records exit code |
| `host_conpty_other.go` | `//go:build !windows` stub: `newConPTY` returns an error ("conpty: unsupported on this OS") so the package compiles on Darwin/Linux |
| `host_main.go` | Cross-platform `RunHost` entrypoint: binds `127.0.0.1:0`, calls `newConPTY`, prints `READY:<pid> <port>`, installs SIGTERM/SIGINT, calls `Serve` |
| `host_test.go` | 7 tests using `fakePTY` (in-memory pipes) + real loopback sockets + B1 `MessageParser` |

## Interface seam

```go
type ptyConn interface {
    io.Reader
    io.Writer
    Resize(cols, rows int) error
    Close() error
    Done() <-chan struct{}
    ExitCode() (int, bool)
    PID() int
}
```

Real impl (`conptyConn`) is in `host_conpty_windows.go` (`//go:build windows`). Tests use `fakePTY` (defined in `host_test.go`), never the stub. The stub only exists so `go build ./...` succeeds on Darwin/Linux.

## Build-tag split

- `host_conpty_windows.go`: `//go:build windows` - imports `github.com/aymanbagabas/go-pty`; only file that touches the go-pty dependency.
- `host_conpty_other.go`: `//go:build !windows` - pure stdlib, no dependencies.
- `host.go`, `host_main.go`: no build tags (cross-platform, stdlib `net` only).
- `host_test.go`: no build tags (runs on Darwin via `fakePTY`).

## Loopback TCP transport (deliberate deviation from pty-host.ts)

pty-host.ts uses a Windows named pipe. This Go port uses `net.Listen("tcp", "127.0.0.1:0")` instead:
- No new dependency (stdlib `net`; no go-winio).
- Testable on Darwin with real sockets.
- Port is dynamically assigned (`127.0.0.1:0`); extracted via `ln.Addr().(*net.TCPAddr).Port` and reported in the `READY:<pid> <port>` line.
- Security note (marked with `ponytail:` comment in `host_main.go`): any local process on the host can connect to the port. A per-session random token handshake is the upgrade path for multi-user isolation.

## Behavior fidelity vs pty-host.ts

| Behavior | Implemented |
|---|---|
| PTY output pumped to ring + broadcast as MsgTerminalData | Yes (`pumpPTY`) |
| New client receives ring snapshot as one MsgTerminalData frame | Yes (`handleConn` replay before adding to set) |
| MsgTerminalInput forwarded to PTY (if not exited) | Yes |
| MsgResize parsed + forwarded (if not exited); malformed ignored | Yes |
| MsgGetOutputReq with default 50 lines, returns MsgGetOutputRes | Yes |
| MsgStatusReq returns {alive, pid, exitCode?} | Yes |
| MsgKillReq triggers graceful shutdown | Yes |
| PTY exit: FlushPartial, broadcast MsgStatusRes{alive:false}, keep-alive | Yes |
| Graceful shutdown: ConPTY Close first, 50ms grace, close clients, close listener | Yes |
| Shutdown is idempotent (sync.Once) | Yes |
| SIGTERM/SIGINT trigger shutdown in RunHost | Yes |

## Verification outputs

```
go build ./...                                 PASS (Darwin)
GOOS=windows go build ./...                    PASS (cross-compile)
GOOS=linux go build ./...                      PASS (cross-compile)
go test -race ./internal/adapters/runtime/conpty/...   34 passed in 2 packages
go vet ./internal/adapters/runtime/conpty/...  PASS (no issues)
```

## Tests implemented

1. `TestScrollbackReplay` - seeded ring delivered as first frame to new client.
2. `TestFanOut` - two simultaneous clients both receive PTY output.
3. `TestTerminalInput` - MsgTerminalInput bytes reach fakePTY's input pipe.
4. `TestResize` - MsgResize calls fakePTY.Resize(132, 40).
5. `TestGetOutputReq` - MsgGetOutputReq returns ring.Tail(n) as MsgGetOutputRes.
6. `TestStatusReq_AliveAndExited` - alive:true while running; alive:false + exitCode after signalExit; listener stays open (keep-alive).
7. `TestKillReq` - MsgKillReq calls pty.Close(), closes listener, Serve returns.
8. `TestShutdownViaCtxCancel` - ctx cancel triggers graceful shutdown.

Tests use channels (not sleeps) for synchronization; deterministic under -race.

## Concerns

### Windows-only wiring not runtime-verified

`host_conpty_windows.go` cross-compiles cleanly (`GOOS=windows go build ./...` passes), but the real `conptyConn` path could not be exercised on this Darwin machine. Specific concerns:

1. `go-pty`'s `New()` on Windows returns `ConPty` (confirmed from `pty_windows.go`); the type-assert `p.(gopty.ConPty)` should succeed. If a future go-pty version changes this, the assert will panic at runtime.
2. `cmd.Environ()` on Windows (inside `newConPTY`) calls `exec.Command(shellCmd).Environ()` to inherit the parent environment. This is a lightweight approach but spawns a short-lived process on Windows just to get the env; a cleaner path is `os.Environ()`. Left as-is to match node-pty's `process.env` pass-through semantics.
3. The `conptyConn.Close()` only closes the ConPTY handle (`pty.Close()`), not the cmd process explicitly. On Windows, closing the ConPTY handle causes the child process to receive EOF and exit on its own; this matches how node-pty handles it. If the child does not exit, there is no explicit kill here.

These are not blocking for Darwin CI; they should be verified in a Windows CI job before merging to production.

---

## Review follow-up (1 Important + 2 Minor fixes) — commit 6cd6e2a

### 1. Important (real bug): scrollback/live-output ordering could drop bytes
`host.go` `handleConn` took the ring `Snapshot()` and added the conn to the
broadcast set under two separate `h.mu` acquisitions. A PTY chunk arriving in
that gap was in neither the snapshot nor that client's broadcast and was
silently dropped (an internal hole in the client's stream).

Fix: acquire `h.mu` once, take the snapshot, write it to the conn (only if
non-empty), then add the conn to `clients`, then release. `broadcast()` also
takes `h.mu`, so it cannot interleave: every chunk is either already in the
snapshot or broadcast strictly after the conn joins the set. Added a `ponytail:`
note that the snapshot write happens under the lock (bounded by `MaxOutputLines`;
upgrade path is a per-client send queue).

Regression test: `TestScrollbackLiveOrdering_NoDrop` (host_test.go). The PTY
emits a contiguous stream of numbered lines (`[NNNN]\n`) while a client connects;
the test asserts the client's received stream has no internal gap in the line
indices and reaches the final chunk. Verified it **reliably fails against the old
two-step code** (reverted temporarily: 9/20 iterations failed with
"non-contiguous line indices (dropped chunk)", e.g. "15 followed by 17") and
**passes under `-race -count=20`** with the fix.

### 2. Minor (faithfulness): Windows Close() had no Kill fallback
`host_conpty_windows.go` `conptyConn.Close()` now also calls
`c.cmd.Process.Kill()` (nil-guarded) in addition to `pty.Close()`, so a child
that ignores ConPTY EOF still exits and `Done()` fires. Mirrors `pty.kill()` in
pty-host.ts. (windows-tagged; compile-checked via `GOOS=windows go build`.)

### 3. Minor (simplify): redundant env accessor
`host_conpty_windows.go` now uses `os.Environ()` instead of
`exec.Command(shellCmd).Environ()`; dropped the `os/exec` import, added `os`.

### Command outputs (run from `backend/`)

```
$ go build ./...                 -> Success
$ GOOS=windows go build ./...    -> Success
$ GOOS=linux go build ./...      -> Success
$ go vet ./internal/adapters/runtime/conpty/...   -> No issues found
$ go test -race -count=20 ./internal/adapters/runtime/conpty/...
  700 passed in 2 packages   (35 tests x 20 counts, all green)

Regression-guard verification (old buggy code reverted):
$ go test -race -run TestScrollbackLiveOrdering_NoDrop -count=20 ...
  11 passed, 9 failed  -> "non-contiguous line indices (dropped chunk)"
```

Touched only files under `backend/internal/adapters/runtime/conpty/`. No new deps.
Committed on branch `migrate-zellij-to-tmux-conpty` as `6cd6e2a`.
