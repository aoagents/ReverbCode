# Task B2 Report: Windows pty-host registry

## Status: DONE

## Files created (all under `backend/internal/adapters/runtime/conpty/ptyregistry/`)

- `registry.go` - main package: `Entry` struct, `Register`, `Unregister`, `List`, `Clear`, `readRaw`, `writeRaw`, `registryFile`, `pidAlive` var.
- `pidalive_unix.go` (`//go:build !windows`) - `defaultPidAlive` via `syscall.Kill(pid, 0)`; nil and EPERM = alive, ESRCH = dead.
- `pidalive_windows.go` (`//go:build windows`) - `defaultPidAlive` via `windows.OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, ...)`; success or ERROR_ACCESS_DENIED = alive.
- `ptyregistry_test.go` - 10 tests; all run on Darwin with HOME pointed at `t.TempDir()` and `pidAlive` overridden with a fake.

## Build-tag split

`pidalive_unix.go` covers Darwin, Linux, and every non-Windows target via `!windows`. `pidalive_windows.go` covers Windows only. The package var `var pidAlive = defaultPidAlive` in `registry.go` is the single injection point; tests replace it with a per-test closure.

## Verification outputs (all from `backend/`)

### `go build ./...`
```
Go build: Success
```

### `GOOS=windows go build ./...`
```
Go build: Success
```

### `GOOS=linux go build ./...`
```
Go build: Success
```

### `go test ./internal/adapters/runtime/conpty/ptyregistry/...`
```
Go test: 10 passed in 1 packages
```
Tests covered: Register+List, register-replaces-same-ID, Unregister-removes, Unregister-no-op-when-absent, List-prunes-dead-PIDs (and rewrites file), empty-result-deletes-file, Clear-deletes-file, malformed-JSON-returns-empty, missing-file-returns-empty, atomic-write-produces-valid-JSON.

### `go vet ./internal/adapters/runtime/conpty/ptyregistry/...`
```
Go vet: No issues found
```

## Design fidelity notes

- `readRaw` drops entries missing sessionId, ptyHostPid, or pipePath (mirrors TS filter).
- `writeRaw` uses temp-file + `os.Rename` (atomic on same filesystem); deletes the file when list is empty (mirrors TS `writeRaw`).
- `Register` is filter-then-append (same-SessionID replaced, not duplicated).
- `registeredAt` is caller-supplied; `time.Now()` is never called inside the registry.
- `registryFile()` is a function (not a const), so `t.Setenv("HOME", dir)` redirects all I/O.
- `golang.org/x/sys/windows` used only in `pidalive_windows.go`; no new go.mod dependency added.

## Concerns

None. All five verification commands green.
