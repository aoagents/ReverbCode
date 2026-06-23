# Task B5 Report: terminal Attach/Stream interface change

Status: DONE

## Summary
Evolved the terminal layer from argv-based attach (`PTYSource.AttachCommand` +
injected `spawnFunc`) to stream-based attach (`Source` embedding
`ports.Attacher`). tmux/zellij keep spawning their attach CLI on a local PTY
(now via the shared `ptyexec.Spawn`); conpty attaches by dialing its loopback
pty-host directly. Behavior on the tmux/zellij paths is preserved exactly.

## Interface shapes

`internal/ports/outbound.go` (added `io` import):
```go
type Stream interface {
    io.ReadWriteCloser
    Resize(rows, cols uint16) error
}
type Attacher interface {
    Attach(ctx context.Context, handle RuntimeHandle, rows, cols uint16) (Stream, error)
}
```

`internal/terminal/attachment.go`:
```go
type Source interface {
    ports.Attacher
    IsAlive(ctx context.Context, handle ports.RuntimeHandle) (bool, error)
}
```
`Runtime`/`RuntimeHandle` untouched.

## Files changed / moved

### New shared package `internal/adapters/runtime/ptyexec`
- `spawn_unix.go` — `Spawn(ctx, argv, env, rows, cols) (ports.Stream, error)`,
  the verbatim creack/pty path: StartWithSize, the Setsize+explicit-SIGWINCH
  self-heal, the SIGTERM->detachGrace->SIGKILL idempotent Close, ctx-cancel
  closes the PTY. Concrete type `creackPTY` satisfies `ports.Stream`.
- `spawn_windows.go` — verbatim ConPTY path (`conPTYProcess`): Resize-on-birth,
  EOF-then-Kill Close. Same `Spawn` signature, build-tag split preserved.
- `spawn_unix_test.go` / `spawn_windows_test.go` — moved from terminal,
  re-pointed at `Spawn`/`detachGrace`/`ports.Stream`. Generic "attach client"
  wording replaces "zellij" in the moved comments. (Windows test renamed
  `TestDefaultSpawnWindows*` -> `TestSpawnWindows*`.)

### Deleted from terminal
`pty_unix.go`, `pty_windows.go`, `pty_unix_test.go`, `pty_windows_test.go`
(moved into ptyexec).

### `internal/ports/outbound.go`
Added `Stream` + `Attacher` + `io` import.

### `internal/terminal/attachment.go`
- `PTYSource` -> `Source` (embeds `ports.Attacher` + IsAlive).
- Deleted the `ptyProcess` interface and the `spawnFunc` type; the run loop and
  helpers (`copyOut`/`setPTY`/`clearPTY`) now use `ports.Stream` directly.
- Run loop: dropped the `AttachCommand` step; after the IsAlive gate it does
  `rows, cols := a.size(); p, err := a.src.Attach(ctx, a.handle, rows, cols)`.
  Reattach/backoff/size/markExited semantics are byte-for-byte identical. An
  Attach error now shares the spawn-error retry policy (increment failures, back
  off, cap at maxReattach) — the old immediate-fail on AttachCommand error is
  gone because there is no separate argv-build step anymore.
- Stale "Zellij"/"argv" comments made runtime-neutral.

### `internal/terminal/manager.go`
Removed `spawn spawnFunc` field, the `defaultSpawn` default, and `WithSpawn`.
`newAttachment` lost its spawn parameter. `src` is now a `Source`. Comments made
runtime-neutral.

### tmux (`internal/adapters/runtime/tmux/tmux.go`)
`AttachCommand` -> unexported `attachCommand` (argv builder unchanged); new
`Attach(ctx, handle, rows, cols)` = `ptyexec.Spawn(ctx, argv, nil, rows, cols)`.
Added `var _ ports.Attacher = (*Runtime)(nil)` and the ptyexec import.

### zellij (`internal/adapters/runtime/zellij/zellij.go`)
Same pattern; `attachCommand` returns argv AND the Windows env block, both passed
to `ptyexec.Spawn`. Added the `ports.Attacher` assertion + ptyexec import.

### conpty (`internal/adapters/runtime/conpty/attach.go`, new)
`Attach` resolves the session addr (existing `resolve()`), `dialHost`, sends an
initial `MsgResize` when rows/cols > 0, and returns a `*loopbackStream`:
- a `pump` goroutine feeds a `MessageParser`; each `MsgTerminalData` payload is
  written into an `io.Pipe` (the host sends the scrollback Snapshot as the first
  such frame, so `Read` yields the replay first). Pipe back-pressure preserves
  order; conn EOF closes the pipe with the error.
- `Write(p)` -> `EncodeMessage(MsgTerminalInput, p)` on the conn.
- `Resize(rows, cols)` -> `EncodeMessage(MsgResize, json{cols,rows})`.
- `Close()` (sync.Once) closes conn + both pipe ends; a ctx goroutine calls it on
  cancel.
`runtime.go` header/assertion comments de-staled ("Attach in attach.go").

### runtimeselect (`runtimeselect.go`)
Union `Runtime` now embeds `ports.Attacher` in place of the `AttachCommand`
method; tmux + zellij compile-time assertions still hold (they now also assert
Attach). Daemon wiring `terminal.NewManager(runtimeAdapter, ...)` still compiles
because the union satisfies `terminal.Source`.

## Test migration

- `fakes_test.go`: `fakeSource` now implements `Attach` (delegating to an
  embedded `*fakeSpawner` or a custom `attachFn` closure) + IsAlive; the
  `alive/aliveErr/attachErr` knobs are preserved (`attachErr` makes Attach fail).
  `fakeSpawner.spawn` lost its argv/env/ctx params (now `spawn(rows, cols)
  (ports.Stream, error)`); `fakePTY` is a `ports.Stream`. The `argv` knob was
  dropped (argv is no longer surfaced to the terminal layer).
- `attachment_test.go`: helpers lost the spawn arg; each test sets
  `src.spawner = sp` (or `src.attachFn = ...`) instead of passing `sp.spawn`.
  `TestAttachmentFailsWhenAttachCommandErrors` -> `TestAttachmentFailsWhenAttachErrors`:
  retargeted to assert a persistent Attach error backs off and exits after the
  retry budget (cap lowered to keep it fast), since attach errors now share the
  spawn-error retry path. Every other assertion's intent preserved; "spawns"
  wording -> "attaches".
- `manager_test.go`: dropped `WithSpawn`; fakes wired via `src.spawner`/`attachFn`.
  Added the `ports` import. zellij-specific comments made runtime-neutral.
- `logger_test.go`: dropped the spawn arg from `newAttachment`/`NewManager`.
- `attachment_integration_test.go`: still drives the REAL zellij runtime, now via
  its `Attach` (dropped the explicit `defaultSpawn` arg — `*zellij.Runtime`
  satisfies `terminal.Source`). Alt-screen + stty-size assertions unchanged.
- `httpd/terminal_mux_test.go`: `stubSource.AttachCommand` -> `Attach` calling
  `ptyexec.Spawn` (still exercises the genuine creack/pty + wsjson + Serve flow
  on Darwin).
- New `conpty/attach_test.go`: drives `Attach` against an in-process B3 `Serve` +
  `fakePTY` (reusing host_test.go's `serveFixture`): scrollback replay arrives on
  Read; Write reaches `fakePTY.inR`; birth + explicit Resize reach
  `fakePTY.Resize`; unknown session errors. 4 tests.

## Verification (from backend/)
- `go build ./...` — Success
- `GOOS=windows go build ./...` — Success
- `GOOS=linux go build ./...` — Success
- `go vet ./...` — No issues found
- `go test -race ./...` — 1638 passed in 78 packages, 0 failures
- Real integration (not skipped, ran under -race): tmux 3.6b
  `TestRuntimeIntegration` + `TestRuntimeIntegrationExactSessionParsing` pass;
  zellij-backed `TestAttachmentStreamsRealZellijPane` +
  `TestAttachmentReattachAdoptsNewSize` pass (the Darwin attach path works end to
  end through the new `Attach`).

## Concerns
None blocking. One intentional behavior note: a failed Attach is now treated as a
transient/retryable failure (back off + cap) rather than an immediate fail,
because the argv build is folded into Attach. This is the correct semantics for a
dial/exec failure and matches the old spawn-error path; the one test asserting
the old immediate-fail was retargeted accordingly.
