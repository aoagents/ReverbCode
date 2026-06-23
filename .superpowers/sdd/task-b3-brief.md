# Task B3: pty-host serve engine + ConPTY seam (loopback TCP transport)

## Goal
Build the detached "pty-host" that owns the agent's ConPTY and exposes it over a
**localhost loopback TCP socket** (127.0.0.1) using the B1 binary protocol, with
ring-replay to new clients, multi-client fan-out, and graceful shutdown. This ports
agent-orchestrator's `pty-host.ts` behavior, with ONE deliberate transport change:
loopback TCP instead of a Windows named pipe (avoids a new dependency and makes the
whole engine testable on Darwin). The ConPTY itself is behind an interface seam so
the serve engine is fully unit-tested here; only the real go-pty implementation is
Windows-only.

Repo: `/Users/harshitsinghbhandari/Downloads/side-quests/rv-code/ReverbCode`,
module `backend/`, branch `migrate-zellij-to-tmux-conpty`. Package:
`internal/adapters/runtime/conpty` (same package as B1's proto.go/ring.go; reuse
them directly — they are in this package).

## Reference (port the behavior)
`/Users/harshitsinghbhandari/Downloads/side-quests/rv-code/agent-orchestrator/packages/plugins/runtime-process/src/pty-host.ts`
(spawn, READY handshake, appendOutput->ring, broadcast/fan-out, scrollback replay
on connect, the MSG_* handlers, onExit keep-alive, graceful shutdown disposing the
ConPTY before exit). And `index.ts` lines 78-175 for the spawn/READY contract.

## The ConPTY seam (so the engine is testable on Darwin)
Define an interface the engine uses, with a fake for tests and a real go-pty impl:
```go
// ptyConn is the host's handle to the running agent's pseudo-terminal.
type ptyConn interface {
    io.Reader            // PTY output (raw bytes)
    io.Writer            // PTY input (keystrokes)
    Resize(cols, rows int) error
    Close() error        // dispose the ConPTY (graceful) 
    Done() <-chan struct{} // closed when the child process exits
    ExitCode() (int, bool) // (code, true) once exited; (0,false) while running
    PID() int
}
```
- Real impl `conptyConn` in `host_conpty_windows.go` (`//go:build windows`): wraps
  `github.com/aymanbagabas/go-pty` (already a dependency). Create the pty, start the
  shell command, expose Read/Write/Resize/Close, run a goroutine that Waits the cmd
  and records exit code + closes Done().
- Non-windows stub `host_conpty_other.go` (`//go:build !windows`): a `newConPTY`
  that returns an error ("conpty: unsupported on this OS"). Keeps the package
  buildable and the engine importable on Darwin. The TESTS use a fake ptyConn
  defined in the test file, NOT this stub.

## The serve engine (`host.go`, cross-platform, the bulk of the work)
```go
// ServeConfig carries everything the host needs.
type ServeConfig struct {
    SessionID string
    Listener  net.Listener // caller provides (loopback); engine owns Accept loop
    PTY       ptyConn
    Ring      *Ring
}
// Serve runs the host event loop until the listener closes or Shutdown is invoked.
// It pumps PTY output -> ring + broadcast, accepts clients, replays ring snapshot
// to each new client, dispatches client messages, and on PTY exit broadcasts a
// MSG_STATUS_RES{alive:false} while staying up (keep-alive, like tmux). Returns
// when shut down.
func Serve(ctx context.Context, cfg ServeConfig) error
```
Behavior to match pty-host.ts:
- Start a goroutine reading PTY output; for each chunk: `ring.Append(chunk)` then
  broadcast `EncodeMessage(MsgTerminalData, chunk)` to all clients. Guard the client
  set with a mutex.
- On a new client connection: immediately send the ring Snapshot as one
  MsgTerminalData frame (scrollback replay) if non-empty, then add to the client
  set, and feed its bytes to a per-conn MessageParser.
- Message handlers (mirror handleClientMessage):
  - MsgTerminalInput -> if not exited, `pty.Write(payload)`.
  - MsgResize -> parse ResizePayload JSON, if not exited `pty.Resize(cols, rows)`.
  - MsgGetOutputReq -> parse GetOutputReq (default 50), reply MsgGetOutputRes with
    `ring.Tail(lines)`.
  - MsgStatusReq -> reply MsgStatusRes JSON {alive, pid, exitCode?} (alive = not
    exited).
  - MsgKillReq -> trigger graceful shutdown (below).
- On PTY exit (Done() fires): record exit, `ring.FlushPartial()`, broadcast
  MsgStatusRes{alive:false,...}. DO NOT close the listener (keep-alive: clients can
  still connect and read scrollback; IsAlive stays true until Destroy).
- Graceful shutdown (Shutdown or MsgKillReq or ctx cancel): dispose the ConPTY
  (`pty.Close()`) FIRST, then close all client conns, then close the listener.
  Mirror the 50ms grace: after disposing the ConPTY, wait ~50ms before returning so
  the OS ConPTY helper can release cleanly (port the `setTimeout(...,50)` note;
  on Windows this avoids the 0x800700e8 error dialog). Make Serve idempotent on
  shutdown.

## Subcommand entrypoint (`host_main.go`, cross-platform shell, ConPTY part tagged)
```go
// RunHost is the `ao pty-host` entrypoint. argv (after the subcommand name):
//   <sessionId> <cwd> <shellCmd> [shellArg...]
// It binds 127.0.0.1:0, creates the ConPTY (newConPTY), prints "READY:<pid> <port>\n"
// to stdout (the parent reads this to learn the port), installs signal handlers,
// then runs Serve. Returns a process exit code.
func RunHost(args []string, stdout io.Writer) int
```
- Bind `net.Listen("tcp", "127.0.0.1:0")`; extract the port from
  `ln.Addr().(*net.TCPAddr).Port`.
- ponytail: loopback bind only; any local process on this host could connect to the
  port. A per-session random token handshake is the upgrade if multi-user isolation
  is needed. Name this in the comment.
- newConPTY(cwd, shellCmd, shellArgs) -> ptyConn (errors on non-windows stub).
- Print `READY:<pid> <port>` AFTER the listener is up and the pty is created.
- Install SIGTERM/SIGINT (+ SIGBREAK on windows is fine to omit in shared code; the
  windows file can add it) to call Serve's shutdown.

## Tests (`host_test.go`, run fully on Darwin with a FAKE ptyConn + real loopback)
Define a `fakePTY` implementing ptyConn backed by in-memory pipes (e.g. two
io.Pipe pairs or buffers + a channel for Done). Use a real `net.Listen("tcp",
"127.0.0.1:0")` and real `net.Dial` clients. Cover:
- A connecting client receives the scrollback snapshot first (seed the ring, connect,
  read a MsgTerminalData frame equal to the snapshot).
- PTY output is fanned out to two simultaneously-connected clients.
- MsgTerminalInput from a client reaches the fakePTY's input.
- MsgResize calls fakePTY.Resize with the right cols/rows.
- MsgGetOutputReq returns MsgGetOutputRes with ring.Tail(n).
- MsgStatusReq returns alive:true while running; after fakePTY signals exit, a new
  MsgStatusReq returns alive:false with the exit code, and the listener is STILL
  accepting (keep-alive).
- MsgKillReq (or Shutdown) disposes the fakePTY (Close called), drops clients, and
  closes the listener; Serve returns.
Use the B1 MessageParser to decode frames in the test client. Keep timeouts short
and deterministic (use channels, not sleeps, where possible).

## Definition of done (from `backend/`)
- `go build ./...` ; `GOOS=windows go build ./...` ; `GOOS=linux go build ./...` succeed.
- `go test -race ./internal/adapters/runtime/conpty/...` passes (B1 tests + these).
- `go vet ./internal/adapters/runtime/conpty/...` clean.

## Hard rules
- Reuse B1's EncodeMessage/MessageParser/MSG_* and Ring (same package). Do not
  duplicate them.
- Only `github.com/aymanbagabas/go-pty` (already present) in the windows-tagged
  file; NO new go.mod module (no go-winio — we use stdlib net loopback on purpose).
- Touch only files under `backend/internal/adapters/runtime/conpty/`.
- Never use em dashes ("—"). Minimal/idiomatic; mark shortcuts with `ponytail:`.
- Commit on the branch; trailer `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>`.
