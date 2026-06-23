# Task B6 Report: select conpty on Windows, register pty-host, delete zellij

## Status: DONE

Commit: `6a18394` on branch `migrate-zellij-to-tmux-conpty`.

---

## What was done

### Step 1: runtimeselect switched to conpty on Windows

`backend/internal/adapters/runtime/runtimeselect/runtimeselect.go`:
- Package doc updated to "tmux on Darwin/Linux, conpty (ConPTY) on Windows".
- Imports: dropped `zellij`, added `conpty`.
- Windows branch: removed `DefaultSocketDir` + `MkdirAll` + warn block; returns `conpty.New(conpty.Options{})`.
- `log` param renamed to `_` (signature kept stable for all callers; `_ *slog.Logger` signature preserved).
- Compile-time assertion changed from `(*zellij.Runtime)(nil)` to `(*conpty.Runtime)(nil)`. tmux assertion unchanged.

### Step 2: `ao pty-host` subcommand registered

New file `backend/internal/cli/ptyhost.go`:
- `newPtyHostCommand()` returns a hidden cobra command with `DisableFlagParsing: true` so agent shell args with leading dashes are not consumed by cobra.
- `RunE` calls `conpty.RunHost(args, os.Stdout)` and calls `os.Exit(code)` on non-zero return (matching the pattern of how launch handles its subprocess exit codes).
- Wired in `root.go` immediately after `newLaunchCommand(ctx)`.

### Step 3: Zellij package deleted

`git rm -r backend/internal/adapters/runtime/zellij` (6 files removed: commands.go, process\_other.go, process\_windows.go, zellij.go, zellij\_integration\_test.go, zellij\_test.go).

Every reference fixed:

**`cli/doctor.go`**:
- Removed `zellij` import.
- Replaced `checkTerminalRuntime`/`checkZellij` with a version that returns a static `doctorPass` on Windows ("ConPTY (built-in): no external terminal multiplexer required on Windows") and calls `checkTmux` on non-Windows. `checkTmux` function body unchanged.

**`cli/spawn.go`**:
- Removed `zellij` import.
- Windows attach hint changed from `zellij attach <session> + ZELLIJ_SOCKET_DIR` to "Attach from the AO dashboard (ConPTY sessions have no CLI attach command)".
- Non-Windows tmux hint unchanged.

**`daemon/wiring_test.go`**:
- Replaced `zellij` import with `tmux`.
- `TestWiring_StartLifecycleThreadsMessengerIntoLCM`: `zellij.New(zellij.Options{})` replaced with `tmux.New(tmux.Options{})`.
- `TestDaemonZellijSocketDir_LeavesBudgetForSessionNames` deleted entirely (tested a helper that no longer exists).

**`daemon/lifecycle_wiring.go`**:
- Stale comment "zellij.Runtime already implements this via SendMessage" updated to "Both tmux.Runtime and conpty.Runtime implement this via SendMessage". No import was present.

### Step 4: Integration test re-pointed at tmux

`backend/internal/terminal/attachment_integration_test.go` (was zellij-backed, now tmux-backed):
- Build tag `//go:build !windows` preserved.
- `zellij` import replaced with `tmux`.
- `TestAttachmentStreamsRealZellijPane` renamed `TestAttachmentStreamsRealTmuxPane`: creates a tmux session, writes via `rt.Create`, attaches via `rt.Attach` (through `newAttachment`), echoes a marker, verifies it appears in the stream, destroys, verifies `isExited()`. Session killed in `t.Cleanup`.
- `TestAttachmentReattachAdoptsNewSize` kept the same name and intent: client A at 37x115, detaches; client B at 40x148 re-attaches, issues `stty size`, asserts cols > 130. Timeout bumped from 5s to 10s for tmux startup. Session killed in `t.Cleanup`.
- Both tests skip if `tmux` is not in PATH (skip guard kept).

---

## Verification outputs

### grep -rn "runtime/zellij" . (from backend/)

```
(empty - exit code 1, no matches)
```

### go build ./...

```
Go build: Success
```

### GOOS=windows go build ./...

```
Go build: Success
```

### GOOS=linux go build ./...

```
Go build: Success
```

### go test -race ./...

```
Go test: 1607 passed in 77 packages
```

The tmux-backed integration tests ran (not skipped) and passed:
- `TestAttachmentStreamsRealTmuxPane`
- `TestAttachmentReattachAdoptsNewSize`

### go vet ./...

```
Go vet: No issues found
```

---

## Concerns

None. All changes are surgical; the union interface and all consumer interfaces are intact. `internal/agentlaunch` is still imported by `cli/launch.go` and was not touched. The remaining "zellij" strings in the codebase are comments only (in tmux.go, doc.go, conpty files, ports, agent adapters) and do not affect the build or runtime behavior.
