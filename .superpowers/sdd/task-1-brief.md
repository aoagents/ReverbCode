# Task 1: tmux runtime adapter

## Goal
Create a new Go package `internal/adapters/runtime/tmux` that drives agent
sessions through the **tmux** CLI, implementing the SAME method set the existing
zellij adapter exposes, so it is a drop-in replacement on Darwin/Linux. This task
adds the package and its tests ONLY. It does NOT wire it into the daemon and does
NOT delete zellij (that is Task 2). The build must stay green.

Module path: `github.com/aoagents/agent-orchestrator/backend`
Work inside: `/Users/harshitsinghbhandari/Downloads/side-quests/rv-code/ReverbCode/backend`

## The template to mirror
The zellij adapter is your structural template. READ THESE FIRST:
- `internal/adapters/runtime/zellij/zellij.go` (Runtime struct, Options, New, the
  `runner` interface + `execRunner`, Create/Destroy/IsAlive/SendMessage/GetOutput/
  AttachCommand, session-name sanitization, `chunks`, `tailLines`,
  `trimTrailingBlankLines`, `validateEnvKeys`, `sortedKeys`, `commandError`).
- `internal/adapters/runtime/zellij/commands.go` (arg builders + shell quoting).
- `internal/adapters/runtime/zellij/zellij_test.go` (the `fakeRunner` test seam:
  `type fakeRunner struct { calls []runnerCall; outputs [][]byte; err error }`,
  injected via `r.runner = &fakeRunner{...}`). Mirror this exactly for tmux tests.

Reuse the same idioms (Go style, comment density, error wrapping `fmt.Errorf("tmux
runtime: ...: %w", err)`). Match the surrounding code.

## Interface the package must satisfy
The package's `Runtime` type must implement, with these EXACT signatures (they are
the existing consumer contracts in the repo — verify against `internal/ports/
outbound.go`, `internal/terminal/attachment.go` `PTYSource`, `internal/review/
launcher.go` `reviewerRuntime`, `internal/daemon/lifecycle_wiring.go`
`runtimeMessageSender`):

```go
func New(opts Options) *Runtime
func (r *Runtime) Create(ctx context.Context, cfg ports.RuntimeConfig) (ports.RuntimeHandle, error)
func (r *Runtime) Destroy(ctx context.Context, handle ports.RuntimeHandle) error
func (r *Runtime) IsAlive(ctx context.Context, handle ports.RuntimeHandle) (bool, error)
func (r *Runtime) SendMessage(ctx context.Context, handle ports.RuntimeHandle, message string) error
func (r *Runtime) GetOutput(ctx context.Context, handle ports.RuntimeHandle, lines int) (string, error)
func (r *Runtime) AttachCommand(handle ports.RuntimeHandle) (argv []string, env []string, err error)
```

Add a compile-time assertion: `var _ ports.Runtime = (*Runtime)(nil)`.

`ports.RuntimeConfig` fields: `SessionID domain.SessionID`, `WorkspacePath string`,
`Argv []string`, `Env map[string]string`. `ports.RuntimeHandle{ ID string }`.

## tmux command mapping (the substance)
Use a `runner` interface identical in shape to zellij's (`Run(ctx, env, name,
args...) ([]byte, error)` and `Start(env, name, args...) error`) with an
`execRunner` default, so tests inject a `fakeRunner`. The tmux binary defaults to
`"tmux"` (allow `Options.Binary` override; resolve via `exec.LookPath`).

- **Create**: validate session id (sanitize like zellij's `SessionName`; tmux
  session names must not contain `.` or `:` — those are window/pane separators —
  and must be non-empty; reuse the same sanitize-to-`[A-Za-z0-9_-]`+hash approach).
  Validate WorkspacePath non-empty, Argv non-empty, env keys (port
  `validateEnvKeys`). Then:
  `tmux new-session -d -s <id> -x 220 -y 50 -c <workspacePath> <launch>`
  where `<launch>` runs the agent then **execs a keep-alive shell so the tmux
  session survives the agent exiting** (this is the whole reason a multiplexer is
  used). Build the launch as a single shell command string passed to
  `sh -c` (or the configured shell), of the form:
  `export K=V; ...; export PATH=...; <quoted-argv>; exec "${SHELL:-/bin/sh}" -i`
  Reuse zellij's `wrapLaunchCommandUnix` quoting approach (single-quote each env
  value and each argv word). Pass env to the agent via the exported-vars prefix
  (do NOT rely on tmux `-e`, which has its own quirks). After creating, set
  `tmux set-option -t <id> status off` (hide the status bar in the embedded web
  terminal). Then verify liveness via IsAlive; on failure, Destroy and return an
  error (mirror zellij's create cleanup discipline). Return
  `ports.RuntimeHandle{ID: <id>}`. NOTE: tmux needs no pane-id discovery — the
  handle is just the session id. Keep the handle format simple (the session id);
  do NOT invent a `session/pane` split unless a consumer requires it (none does —
  verify).
- **Destroy**: `tmux kill-session -t <id>`. Treat "session not found"/"can't find
  session" stderr on a non-zero exit as success (idempotent), mirroring zellij's
  `deleteSessionMissingOutput`.
- **IsAlive**: `tmux has-session -t <id>`. Exit 0 => alive. A non-zero exit whose
  output says the session/server is missing ("can't find session", "no server
  running", "error connecting") => definitively `false, nil`. Any OTHER error =>
  `false, err` (a probe failure, NOT proof of death) — this distinction matters
  because the reaper feeds the lifecycle manager and must not kill a session on a
  transient probe error. Mirror zellij's IsAlive contract precisely.
- **SendMessage**: send literal text then Enter. Use
  `tmux send-keys -t <id> -l <chunk>` for the literal text (the `-l` flag stops
  tmux interpreting words like "Enter"/"C-c" as key names), chunked via the ported
  `chunks(message, chunkSize)` helper, then `tmux send-keys -t <id> Enter` to
  submit. (Per the agent-orchestrator reference, multiline text can alternatively
  go via `load-buffer -`/`paste-buffer`, but `send-keys -l` chunked is simpler and
  correct — use it. Mark any simplification with a `ponytail:` comment.)
- **GetOutput**: `tmux capture-pane -t <id> -p -S -<lines>` (the `-S -<n>` starts n
  lines back in history; `-p` prints to stdout). Then apply the ported
  `trimTrailingBlankLines` + `tailLines(out, lines)`. `lines <= 0` => error, like
  zellij.
- **AttachCommand**: return argv `["tmux", "attach-session", "-t", <id>]` (use the
  resolved binary path as argv[0]) and a `nil` env block (no per-session socket dir
  needed for tmux — unlike zellij's Windows ConPTY env). The terminal layer's
  existing Unix `defaultSpawn` (creack/pty) will spawn this unchanged.

## Options
```go
type Options struct {
    Binary    string        // default "tmux" (resolved via exec.LookPath)
    Shell     string        // default $SHELL else /bin/sh
    Timeout   time.Duration // default 5s
    ChunkSize int           // default 16*1024
}
```

## Tests (REQUIRED — this is the runnable check)
1. **Unit tests** `tmux_test.go` using a `fakeRunner` (copy the struct shape from
   zellij_test.go: records calls, returns scripted outputs/err). Cover: command
   args for Create (new-session + status off), Destroy, IsAlive (alive / missing =>
   false,nil / transient error => err), SendMessage (chunking + -l + Enter),
   GetOutput (tail/trim), AttachCommand argv, session-name sanitization, env-key
   validation, the keep-alive shell is present in the launch command, env vars are
   exported in the launch command.
2. **Integration test** `tmux_integration_test.go` gated on
   `if _, err := exec.LookPath("tmux"); err != nil { t.Skip(...) }` (mirror
   `zellij_integration_test.go`). Real end-to-end: Create a session running a
   trivial command (e.g. `sh -c 'echo hello; ...'` via Argv), assert IsAlive true,
   SendMessage, GetOutput contains expected text, Destroy, then IsAlive false.
   Use a unique session id (incorporate the test name) and ALWAYS Destroy in a
   `t.Cleanup` so a failed test never leaks a tmux session on the dev machine.
   tmux 3.6b IS installed here, so this test WILL run — make it pass.

## Constraints / definition of done
- `cd backend && go build ./... ` succeeds.
- `cd backend && go test ./internal/adapters/runtime/tmux/...` passes (unit +
  integration), and `go vet ./internal/adapters/runtime/tmux/...` is clean.
- Do NOT modify any file outside the new `internal/adapters/runtime/tmux/` package.
- Do NOT add new dependencies (tmux is shelled out via os/exec; no Go module
  needed). go.mod must be unchanged.
- Follow the user's hard rule: **never use em dashes** in code, comments, or commit
  messages. Use periods/commas/parentheses.
- Keep it lazy/minimal (ponytail): no speculative abstraction, no pane-id machinery
  tmux doesn't need, no config the daemon won't set. Mark any deliberate shortcut
  with a `ponytail:` comment naming the ceiling.
- Commit your work on the current branch (`migrate-zellij-to-tmux-conpty`) with a
  clear message. git author is already configured.
