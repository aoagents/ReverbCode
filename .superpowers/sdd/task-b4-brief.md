# Task B4: conpty runtime adapter (loopback pty-client + Create/Destroy/IsAlive/SendMessage/GetOutput)

## Goal
Implement the conpty Runtime adapter that drives sessions via the B3 pty-host over
loopback TCP. It spawns a detached pty-host per session, and talks to it with the
B1 protocol. This task implements everything EXCEPT terminal attach (the
`Attach`/`Stream` method comes in B5, after the terminal interface changes). Ports
agent-orchestrator's `runtime-process/src/index.ts` (Windows branch) and
`pty-client.ts`. The process-spawn is behind a seam so the whole adapter is
unit-tested on this Darwin machine against an in-process B3 `Serve` + fake PTY.

Repo: `/Users/harshitsinghbhandari/Downloads/side-quests/rv-code/ReverbCode`,
module `backend/`, branch `migrate-zellij-to-tmux-conpty`. Package:
`internal/adapters/runtime/conpty` (same package as B1/B3). Add `runtime.go`,
`client.go`, `spawn.go` (+ a windows-tagged spawn file) and tests.

## References (port faithfully — read them)
- `agent-orchestrator/packages/plugins/runtime-process/src/pty-client.ts`
  (connect, ptyHostSendMessage chunking 512 chars/15ms + 300ms Enter delay,
  ptyHostGetOutput, ptyHostIsAlive STATUS probe, ptyHostKill).
- `agent-orchestrator/packages/plugins/runtime-process/src/index.ts` lines 56-175
  (Windows create: spawn detached, READY handshake, register in registry, return
  handle) and 269-310 (destroy: kill via pipe, 500ms grace, force-kill, unregister).

## Session model (important — ReverbCode specifics)
`ports.RuntimeHandle` is just `{ID string}` (opaque, no data map). The terminal
layer constructs handles as `ports.RuntimeHandle{ID: <sessionID>}` from the bare
session id, while the reaper/messenger pass the stored handle id (= what Create
returns). So: **Create returns `ports.RuntimeHandle{ID: <sessionID>}`** (bare), and
the adapter resolves a session by id through:
1. an in-memory `map[string]*hostSession` (sessionID -> {addr, ptyHostPID}),
   populated by Create; then
2. a fallback lookup in the B2 registry (`ptyregistry.List()` / a by-id helper) so
   a daemon that restarted (empty map) can still reach a detached pty-host that is
   still listening. Store the loopback address (e.g. "127.0.0.1:54321") in the
   registry Entry.PipePath field and the ptyHostPID in Entry.PtyHostPID.

Validate session ids with `^[a-zA-Z0-9_-]+$` (port agent-orchestrator's
assertValidSessionId); reject invalid ones from Create.

## The spawn seam (`spawn.go`)
```go
// hostSpawner starts a detached pty-host for the session and returns its loopback
// address ("127.0.0.1:PORT") and OS pid once it prints READY. Injectable for tests.
type hostSpawner func(ctx context.Context, sessionID, cwd string, argv []string, env map[string]string) (addr string, pid int, err error)
```
Default impl `defaultSpawnHost`:
- Resolve the current executable (`os.Executable()`); build argv:
  `<exe> pty-host <sessionID> <cwd> <shellCmd> <shellArg...>` where the agent argv
  is wrapped so the shell runs it then keeps the session alive. ponytail: reuse the
  existing Windows launch approach if simplest; at minimum the host's shell must
  exec the agent argv. (The pty-host subcommand registration in the CLI is B6; this
  task only needs to produce the correct argv and spawn it.)
- Start it DETACHED so it survives the daemon exit (mirror `detached: true`,
  `windowsHide: true`). On non-windows use a stub that returns an error
  ("conpty spawn: unsupported on this OS") in a `//go:build !windows` file; the real
  detached-spawn (with the Windows process-creation flags via golang.org/x/sys/
  windows, already present) goes in a `//go:build windows` file. Read stdout for
  `READY:<pid> <port>` with a 10s timeout, then unref/detach.
- Tests DO NOT use defaultSpawnHost; they inject a fake spawner that starts a B3
  `Serve` with a fake PTY on a real `127.0.0.1:0` listener and returns that addr.

## pty-client helpers (`client.go`, cross-platform stdlib net, fully testable)
Port pty-client.ts as Go functions that dial the loopback addr each call (short-lived
connections, like the TS):
- `func dialHost(addr string, timeout time.Duration) (net.Conn, error)`
- `func clientSendMessage(addr, message string) error` — chunk message by 512
  runes, write each as MsgTerminalInput frame with 15ms gaps, then 300ms pause, then
  one MsgTerminalInput frame with "\r". (Port ptyHostSendMessage; chunk by rune to
  avoid splitting a UTF-8 codepoint — reuse B1's `chunks`-style splitting if a
  helper exists, else split on rune boundaries.)
- `func clientGetOutput(addr string, lines int) (string, error)` — send
  MsgGetOutputReq{lines}, read frames via MessageParser until MsgGetOutputRes, return
  its text; 3s timeout returns "" (no error) like the TS.
- `func clientIsAlive(addr string) bool` — send MsgStatusReq, read until
  MsgStatusRes, return true if valid JSON received; connect failure -> false; 2s
  timeout -> false. (Mirror ptyHostIsAlive: host reachable == alive, regardless of
  the inner agent's alive flag.)
- `func clientKill(addr string) error` — send MsgKillReq, best-effort; connect
  failure is a no-op (already dead).

## Runtime methods (`runtime.go`)
Implement the struct + these methods (the union the daemon wires, minus Attach which
is B5):
- `New(opts Options) *Runtime` (Options: Timeout, Chunksize defaults; keep minimal).
- `Create(ctx, cfg) (ports.RuntimeHandle, error)`: validate id/workspace/argv/env;
  reject duplicate (map already has id); spawn via the spawner; store in map;
  register in the B2 registry (addr, pid, RFC3339 timestamp passed in). Return
  `{ID: sessionID}`. On spawn failure, clean up the map slot.
- `Destroy(ctx, handle)`: resolve addr+pid; `clientKill(addr)`; poll up to ~500ms
  for the pid to exit (use a pidAlive-style probe; reuse ptyregistry's notion or a
  small local probe); then best-effort force-kill the pid (`os.FindProcess(pid).
  Kill()` — ponytail: kills the host, whose graceful shutdown already disposed the
  child ConPTY; no full process-tree kill, upgrade if orphans appear). Remove from
  map; `ptyregistry.Unregister(sessionID)`. Idempotent: unknown/already-gone session
  returns nil.
- `IsAlive(ctx, handle) (bool, error)`: resolve addr; `clientIsAlive(addr)`.
  Resolve-miss (no map entry, no registry entry) -> `(false, nil)` (definitively
  gone, like a missing session). A dial/probe that errors transiently while an entry
  EXISTS -> return `(false, nil)` only if the host is unreachable (gone); otherwise
  it is alive. Keep the contract: never return an error that the reaper would treat
  as death; if you cannot tell, return `(false, nil)` only when the host is truly
  unreachable. (Match the reaper expectation used by tmux/zellij: definitively-dead
  vs alive; for conpty, unreachable host == dead.)
- `SendMessage(ctx, handle, message)`: resolve addr; `clientSendMessage`.
- `GetOutput(ctx, handle, lines)`: resolve addr; `clientGetOutput`; lines<=0 -> error.
- Add `var _ ports.Runtime = (*Runtime)(nil)`. (Attach is added in B5; do not add it
  here.)

## Tests (run on Darwin against in-process B3 Serve + fake PTY)
Build a test harness: start a B3 `Serve` with a fakePTY on a `127.0.0.1:0` listener
(reuse the fakePTY pattern from host_test.go; you may need to export or duplicate a
minimal fake within the test file). Inject a fake spawner returning that addr+a fake
pid. Cover:
- Create registers the session (map + registry Entry present) and returns
  `{ID: sessionID}`; duplicate Create errors; invalid session id errors.
- SendMessage delivers the chunked text + Enter to the fakePTY input (assert the
  fake received message bytes followed by "\r"); large (>512-rune) message is
  chunked.
- GetOutput returns the host's ring tail.
- IsAlive true while the host serves; false after the host listener is closed /
  session unknown.
- Destroy calls clientKill (fakePTY Close observed), removes the map+registry entry,
  and is idempotent on a second call.
- Resolve-via-registry path: with an empty in-memory map but a registry entry
  pointing at a live in-process host, IsAlive/SendMessage still work (simulates a
  daemon restart).

## Definition of done (from `backend/`)
- `go build ./...` ; `GOOS=windows go build ./...` ; `GOOS=linux go build ./...` succeed.
- `go test -race ./internal/adapters/runtime/conpty/...` passes.
- `go vet ./internal/adapters/runtime/conpty/...` clean.

## Hard rules
- Reuse B1 (proto/ring), B2 (ptyregistry), B3 (Serve/fakePTY pattern). No new go.mod
  module (golang.org/x/sys/windows already present; windows-tagged spawn file only).
- Touch only files under `backend/internal/adapters/runtime/conpty/`.
- Never use em dashes ("—"). Minimal/idiomatic; mark shortcuts with `ponytail:`.
- Commit on the branch; trailer `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>`.
