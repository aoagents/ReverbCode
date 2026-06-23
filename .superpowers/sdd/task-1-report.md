# Task 1 Report: tmux Runtime Adapter

## What was built

New package `backend/internal/adapters/runtime/tmux` implementing `ports.Runtime`
via the tmux CLI. Four files were created (no existing files modified):

- `backend/internal/adapters/runtime/tmux/tmux.go` - Runtime struct, Options, New,
  Create/Destroy/IsAlive/SendMessage/GetOutput/AttachCommand, session-name
  sanitization, helpers (chunks, tailLines, trimTrailingBlankLines,
  validateEnvKeys, sortedKeys, shellQuote, buildLaunchCommand, commandError).
- `backend/internal/adapters/runtime/tmux/commands.go` - Arg builders for all tmux
  subcommands (newSessionArgs, setStatusOffArgs, killSessionArgs, hasSessionArgs,
  sendKeysLiteralArgs, sendEnterArgs, capturePaneArgs, exactSessionTarget).
- `backend/internal/adapters/runtime/tmux/tmux_test.go` - 32 unit tests via fakeRunner.
- `backend/internal/adapters/runtime/tmux/tmux_integration_test.go` - 2 integration
  tests gated on `exec.LookPath("tmux")`.

## Design choices

1. Handle format: plain session id string (no session/pane split). tmux needs no
   pane-id discovery; the handle is just `ports.RuntimeHandle{ID: <sanitized-id>}`.

2. Exact session targeting: `kill-session -t =<id>` and `has-session -t =<id>` use
   tmux's `=` exact-name prefix (supported by session-selection commands in tmux
   3.x) to prevent prefix matching ("foo" matching "foobar"). Commands that use
   pane-targeting syntax (set-option, send-keys, capture-pane) use a plain session
   name because they do not support the `=` prefix.

3. Keep-alive shell: `buildLaunchCommand` appends `; exec ${SHELL:-/bin/sh} -i` so
   the tmux session survives agent exit (the whole reason a multiplexer is used).

4. Send-keys chunking: `send-keys -t <id> -l <chunk>` with `-l` flag sends text
   literally (tmux does not interpret "Enter", "C-c", etc. as key names). Chunked
   via ported `chunks()` helper with 16 KB default.

5. `sessionMissingOutput` covers: "can't find session", "no server running",
   "error connecting", "session not found". Both `killSessionMissingOutput` and the
   IsAlive path use this to distinguish definitive-dead from probe-error.

6. `AttachCommand` returns `["tmux", "attach-session", "-t", id]` with nil env
   (no per-session socket dir needed unlike zellij's Windows path).

7. `ponytail:` comments mark the two deliberate simplifications:
   - send-keys -l chunked vs. load-buffer/paste-buffer (ceiling: very large
     messages are slightly slower; 16 KB default is ample for agent prompts).
   - PATH handling matches the zellij unix path.

## Verification output

```
$ cd backend && go build ./...
# success (no output)

$ go test ./internal/adapters/runtime/tmux/... -v
# 34 tests passed (32 unit + 2 integration with real tmux 3.6b)

$ go vet ./internal/adapters/runtime/tmux/...
# no issues
```

## Concerns

None. The build is green, all 34 tests pass (including the integration tests on
the installed tmux 3.6b), and no files outside the new package were modified.
