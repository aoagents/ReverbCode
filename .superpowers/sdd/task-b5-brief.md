# Task B5: terminal Attach/Stream interface change + tmux/zellij/conpty Attach

## Goal
Evolve the terminal layer from argv-based attach (`PTYSource.AttachCommand` + a
`spawnFunc`) to stream-based attach (`Attacher.Attach(...) Stream`), so the conpty
runtime can attach by dialing its loopback host directly (no argv to exec) while
tmux/zellij keep spawning their attach CLI under the hood. This is the keystone that
makes conpty usable from the dashboard. It is fully testable on this Darwin machine
(terminal unit tests + tmux integration). Keep the build green and ALL tests
passing. This is a refactor of working code: be surgical and preserve behavior on
the tmux/zellij paths exactly.

Repo: `/Users/harshitsinghbhandari/Downloads/side-quests/rv-code/ReverbCode`,
module `backend/`, branch `migrate-zellij-to-tmux-conpty`. Module prefix
`github.com/aoagents/agent-orchestrator/backend`.

## Read first (the code you are changing)
- `internal/terminal/attachment.go` (the run loop: IsAlive gate -> AttachCommand ->
  size -> spawn -> copyOut -> reattach), `manager.go` (spawn/WithSpawn plumbing,
  openTerminal), `pty_unix.go` + `pty_windows.go` (defaultSpawn + creackPTY /
  conPTYProcess), `fakes_test.go` (fakeSource, fakeSpawner), `attachment_test.go`,
  `manager_test.go`, `attachment_integration_test.go`, `logger_test.go`,
  `pty_unix_test.go`, `pty_windows_test.go`.
- `internal/ports/outbound.go` (Runtime, RuntimeHandle).
- `internal/adapters/runtime/tmux/` (AttachCommand), `.../zellij/` (AttachCommand),
  `.../conpty/` (client.go dial + proto), `.../runtimeselect/select.go` (the union).

## Step 1 — define Stream + Attacher in `ports` (`internal/ports/outbound.go`)
```go
// Stream is one live terminal attach: PTY-like bytes plus resize. Returned
// already-open by a Runtime's Attach. tmux/zellij back it with a local PTY around
// their attach CLI; conpty backs it with a loopback connection to the pty-host.
type Stream interface {
    io.ReadWriteCloser
    Resize(rows, cols uint16) error
}

// Attacher opens a fresh attach Stream for a session handle, sized rows x cols from
// birth (0 means size not yet known). ctx cancellation must terminate the stream.
type Attacher interface {
    Attach(ctx context.Context, handle RuntimeHandle, rows, cols uint16) (Stream, error)
}
```
(`io` import added to ports.) Do NOT remove `Runtime`/`RuntimeHandle`.

## Step 2 — extract the PTY spawn into a shared package `internal/adapters/runtime/ptyexec`
Move the existing `defaultSpawn` + `creackPTY` (from terminal/pty_unix.go) and the
ConPTY `conPTYProcess` (from terminal/pty_windows.go) into a new package `ptyexec`,
exported as:
```go
// Spawn starts argv on a local PTY (creack/pty on unix, go-pty ConPTY on windows),
// sized rows x cols from birth when known. env, when non-nil, replaces the inherited
// environment. ctx cancellation closes the PTY via the same graceful path as Close.
func Spawn(ctx context.Context, argv, env []string, rows, cols uint16) (ports.Stream, error)
```
- Preserve EVERY behavior and comment from the originals: StartWithSize, the
  SIGWINCH-after-Setsize self-heal on unix, the SIGTERM->grace->SIGKILL detach on
  unix (`detachGrace`), the ConPTY EOF-then-Kill close on windows. The returned
  concrete types must satisfy `ports.Stream` (they already have Read/Write/Close +
  Resize). Keep the unix/windows build-tag split (`spawn_unix.go`/`spawn_windows.go`).
- Move pty_unix_test.go / pty_windows_test.go into ptyexec as well (adjust package +
  any unexported references). These tests must still pass.
- The terminal package no longer needs defaultSpawn after Step 3; delete the moved
  files from terminal.

## Step 3 — rewire the terminal layer to Attach (`attachment.go`, `manager.go`)
- Replace `PTYSource` with:
```go
// Source is what the terminal needs from the runtime: open an attach Stream and a
// liveness check used to decide whether a dropped Stream should be re-attached or
// treated as a clean exit.
type Source interface {
    ports.Attacher
    IsAlive(ctx context.Context, handle ports.RuntimeHandle) (bool, error)
}
```
  (Rename PTYSource -> Source throughout terminal, or keep the name PTYSource but
  change its methods; pick one and be consistent. Update doc comments that describe
  "argv".)
- In `attachment`: delete the `spawn spawnFunc` field and the `spawnFunc` type.
  Change the run loop: after the IsAlive gate, do
  `rows, cols := a.size(); p, err := a.src.Attach(ctx, a.handle, rows, cols)`
  instead of AttachCommand + spawn. `p` is a `ports.Stream`; the rest of the loop
  (setPTY/copyOut/clearPTY/Close/reattach/backoff) is UNCHANGED. The local
  `ptyProcess` interface in attachment.go can be replaced by `ports.Stream` (same
  shape) or kept as an alias; prefer using `ports.Stream` directly.
  Keep the failure handling: an Attach error increments failures and backs off (same
  as the old spawn error path); a definitive `!alive` still markExited.
- In `manager.go`: delete `spawn spawnFunc`, the `defaultSpawn` default, and
  `WithSpawn`. `newAttachment` loses its spawn parameter. `NewManager(src, events,
  log, opts...)` keeps `src` (now a `Source`). If tests need to inject a fake, they
  inject it via `src` (already the first arg) — so `WithSpawn` is removed and tests
  pass a fake `Source` whose `Attach` returns a fake `Stream`.

## Step 4 — implement Attach on the three adapters
- **tmux** (`internal/adapters/runtime/tmux`): add
  `func (r *Runtime) Attach(ctx, handle, rows, cols) (ports.Stream, error)`:
  reuse the existing argv builder (the body of AttachCommand: `tmux attach-session
  -t <id>`), then `return ptyexec.Spawn(ctx, argv, nil, rows, cols)`. You MAY keep
  AttachCommand as an unexported helper that builds the argv, or inline it. Add
  `var _ ports.Attacher = (*Runtime)(nil)`. Keep AttachCommand removed from the
  public surface only if nothing else uses it (the CLI/doctor do not).
- **zellij** (`internal/adapters/runtime/zellij`): same pattern. zellij's
  AttachCommand returns argv AND an env block (Windows ConPTY socket dir); pass both
  to `ptyexec.Spawn(ctx, argv, env, rows, cols)`. Add the `var _ ports.Attacher`
  assertion. (zellij stays in the tree; Windows still builds it until B6.)
- **conpty** (`internal/adapters/runtime/conpty`): add
  `func (r *Runtime) Attach(ctx, handle, rows, cols) (ports.Stream, error)`:
  resolve the session addr (the existing resolve()), `dialHost(addr, ...)`, send an
  initial MsgResize if rows/cols > 0, and return a `*loopbackStream` that:
  - runs a goroutine reading frames via a B1 MessageParser; for each MsgTerminalData
    it writes the payload into an internal io.Pipe; `Read` drains that pipe. (The
    host sends the scrollback Snapshot as the first MsgTerminalData on connect, so
    Read naturally yields the replay first.)
  - `Write(p)` sends `EncodeMessage(MsgTerminalInput, p)` to the conn.
  - `Resize(rows, cols)` sends `EncodeMessage(MsgResize, json{cols,rows})`.
  - `Close()` closes the conn and the pipe; ctx cancellation also closes it.
  Add `var _ ports.Attacher = (*Runtime)(nil)`. This is fully testable on Darwin
  against an in-process B3 Serve + fakePTY: connect, assert the scrollback replay
  arrives on Read, Write reaches the fakePTY input, Resize reaches fakePTY.Resize.

## Step 5 — update the runtimeselect union (`internal/adapters/runtime/runtimeselect/select.go`)
Replace `AttachCommand(handle) ([]string, []string, error)` in the union `Runtime`
interface with `ports.Attacher` (i.e. `Attach(...)`). Keep the compile-time
assertions for tmux and zellij; they now also assert Attach. The daemon wiring
(terminal.NewManager(runtimeAdapter, ...)) still compiles because the union
satisfies terminal.Source.

## Step 6 — migrate the terminal tests
- `fakes_test.go`: change `fakeSource` to implement `Attach(ctx, handle, rows, cols)
  (ports.Stream, error)` (returning a scripted fake Stream) + IsAlive. Replace
  `fakeSpawner`/fake spawn with a fake `Stream` (in-memory Read/Write/Resize/Close,
  scriptable like the old fake PTY). Preserve the existing `alive/aliveErr/attachErr`
  knobs (attachErr now makes Attach fail).
- `attachment_test.go`, `manager_test.go`, `logger_test.go`,
  `attachment_integration_test.go`: update `newAttachment(...)`/`NewManager(...)`
  call sites to drop the spawn arg / WithSpawn, and to use the fake Source's Attach.
  The integration test (which used the real defaultSpawn + a real runtime) should now
  drive a real adapter's Attach (tmux is available; or keep it pointed at whatever
  runtime it used, adapted to Attach). Keep every assertion's intent. Do not weaken
  tests to pass; if a test asserted argv contents from AttachCommand, re-target it to
  assert the adapter's argv via the adapter's own (possibly unexported) builder or
  drop only that now-meaningless assertion, noting why.

## Definition of done (from `backend/`)
- `go build ./...` ; `GOOS=windows go build ./...` ; `GOOS=linux go build ./...` succeed.
- `go test -race ./...` passes (the WHOLE backend suite, not just the changed pkgs).
- `go vet ./...` clean.
- tmux integration test still passes against real tmux 3.6b (the Darwin attach path
  must still actually work end to end).

## Hard rules
- This refactors verified, working code (the tmux attach path shipped in Phase A).
  Preserve its behavior exactly; the only structural change is argv->Stream. Do not
  alter the reattach/backoff/size/SIGWINCH/detach semantics.
- No new go.mod module. Never use em dashes ("—"). Minimal/idiomatic; mark shortcuts
  with `ponytail:`. Update stale "Zellij"/"argv" comments in the terminal files you
  touch to be runtime-neutral.
- Commit on the branch; trailer `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>`.
