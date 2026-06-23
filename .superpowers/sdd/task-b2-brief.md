# Task B2: Windows pty-host registry (sideband JSON, cross-platform-testable)

## Goal
Port agent-orchestrator's `windows-pty-registry.ts` to Go: a flat JSON sideband
list of live pty-host processes so a stop/sweep can find and graceful-kill them
even when session metadata is lost. The JSON read/write/prune logic is pure Go and
MUST be fully unit-tested and green on this Darwin machine; only the PID-liveness
probe is OS-specific and is isolated behind a tiny build-tagged helper.

Repo: `/Users/harshitsinghbhandari/Downloads/side-quests/rv-code/ReverbCode`,
module in `backend/`, branch `migrate-zellij-to-tmux-conpty`. Module prefix:
`github.com/aoagents/agent-orchestrator/backend`.

New package: `internal/adapters/runtime/conpty/ptyregistry`
(separate sub-package so it has no dependency on the Windows ConPTY code).

## Reference (port faithfully — read it)
`/Users/harshitsinghbhandari/Downloads/side-quests/rv-code/agent-orchestrator/packages/core/src/windows-pty-registry.ts`
(entry shape, register/unregister/getWindowsPtyHosts with auto-prune, clear,
atomic write, isAlive via process.kill(pid,0) with EPERM-means-alive).

## Registry file location
`~/.ao/windows-pty-hosts.json` (agent-orchestrator uses ~/.agent-orchestrator;
ReverbCode's AO home is `~/.ao`). Resolve via `os.UserHomeDir()` joined with
`.ao` and the filename. Resolve it through a small unexported function (NOT a
package-level const) so tests can point HOME at a tempdir (`t.Setenv("HOME", dir)`
on Unix) and exercise real read/write. ponytail: HOME-based resolution is enough;
do not thread an AO_DATA_DIR override here unless a consumer needs it.

## API (exported)
```go
type Entry struct {
    SessionID    string `json:"sessionId"`
    PtyHostPID   int    `json:"ptyHostPid"`
    PipePath     string `json:"pipePath"`
    RegisteredAt string `json:"registeredAt"` // RFC3339; caller passes the timestamp
}

// Register adds or replaces the entry for entry.SessionID. registeredAt is set by
// the caller (pass time.Now().UTC().Format(time.RFC3339)) — do NOT call time.Now()
// inside the registry, so it stays deterministic/testable. Accept it as a param.
func Register(entry Entry) error

// Unregister removes the entry for sessionID. No-op if absent.
func Unregister(sessionID string) error

// List returns all entries whose PtyHostPID is still alive, auto-pruning dead
// ones (rewrites the file if any were pruned). Liveness uses pidAlive (below).
func List() ([]Entry, error)

// Clear deletes the registry file (best-effort; used by tests/recovery).
func Clear() error
```
Behavior to match the TS:
- Read tolerates a missing file (returns empty), and a malformed/non-array file
  (returns empty rather than erroring) — mirror `readRaw`'s defensive filter that
  drops entries missing sessionId/ptyHostPid/pipePath.
- Write is atomic: write to a temp file in the same dir then `os.Rename` over the
  target (rename is atomic on the same filesystem). Create `~/.ao` with 0o700 if
  missing. When the resulting list is empty, delete the file instead of writing
  `[]` (mirror `writeRaw`).
- Register replaces any existing entry with the same SessionID (filter-then-append).

## OS-specific PID liveness (isolate behind build tags)
A package-level `var pidAlive = defaultPidAlive` that List uses, so tests override
it with a fake. Provide `defaultPidAlive` in two build-tagged files:
- `pidalive_unix.go` (`//go:build !windows`): use `syscall.Kill(pid, 0)`; treat
  `nil` and `EPERM` as alive, `ESRCH` (and anything else) as dead. (Mirrors
  process.kill(pid,0) with EPERM-means-alive.)
- `pidalive_windows.go` (`//go:build windows`): port the EPERM-means-alive intent
  using the Windows API. Use `golang.org/x/sys/windows` (already a dependency):
  `OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))`; if it
  succeeds the process is alive (CloseHandle and return true). If it fails with
  ERROR_ACCESS_DENIED treat as ALIVE (exists but not queryable), otherwise dead.
  This file is compile-checked via GOOS=windows but not run here; keep it small and
  obviously correct.

## Tests (`ptyregistry_test.go`, run on Darwin)
Point HOME at `t.TempDir()` and override `pidAlive` with a fake controlled by the
test. Cover:
- Register then List returns the entry (with the fake marking it alive).
- Register replaces a same-sessionID entry (no duplicate).
- Unregister removes; no-op when absent.
- List prunes entries whose pid the fake reports dead, and rewrites the file
  (verify by reading the file back / a second List).
- Empty result deletes the file (Clear, or unregistering the last entry).
- Malformed JSON file -> List returns empty, no error.
- Missing file -> List returns empty, no error.
- Atomicity smoke: Register writes valid JSON parseable back into []Entry.

## Definition of done (from `backend/`)
- `go build ./...` ; `GOOS=windows go build ./...` ; `GOOS=linux go build ./...` all succeed.
- `go test ./internal/adapters/runtime/conpty/ptyregistry/...` passes.
- `go vet ./internal/adapters/runtime/conpty/ptyregistry/...` clean.

## Hard rules
- Touch only files under `backend/internal/adapters/runtime/conpty/ptyregistry/`.
- The only dependency allowed is the already-present `golang.org/x/sys/windows`
  (windows file only). go.mod must not gain a NEW module.
- Never use em dashes ("—") anywhere. Minimal/idiomatic (ponytail).
- Commit on the current branch; message ends with the
  `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>` trailer.
