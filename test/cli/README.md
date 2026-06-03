# `ao` CLI end-to-end tests

These tests drive the **real `ao` binary** the way a user would — `start` →
`status` → `doctor` → `stop`, plus the daemon-control HTTP surface — and assert
the whole thing works. They run against **isolated, throwaway state** (a per-test
temp run-file + data dir + an OS-assigned free loopback port), so they never
touch a developer's real AO installation.

## Two tiers

| Tier | What | Where |
|------|------|-------|
| **Comprehensive (primary)** | A cross-platform Go suite that builds `ao` and exercises the full behaviour. Runs natively on **ubuntu + macOS + windows** — the only way to cover the OS-specific process-detach paths (`setsid` vs `CREATE_NEW_PROCESS_GROUP`) and `os.UserConfigDir()` resolution. | `backend/internal/cli/e2e_test.go` (build tag `e2e`) |
| **Fresh-install (hardening)** | Proves a freshly installed binary works on a clean machine with no Go toolchain and no developer state. | `test/cli/Dockerfile` + `test/cli/install-check.sh` |

## Run it

**The Go suite (fastest, cross-platform):**
```bash
cd backend
go test -tags e2e ./internal/cli/...              # run it
go test -tags e2e -v -run TestE2E ./internal/cli/...   # verbose: prints every command + output
```
It builds its own `ao` binary; `git` must be on PATH (required by `doctor`).
`-v` logs each `ao` invocation and its full output, which is the audit trail you
get for free from `go test`.

**Fresh-machine install, in a clean container:**
```bash
docker build -f test/cli/Dockerfile -t ao-cli-smoke .
docker run --rm --init ao-cli-smoke
```
> `--init` gives the container a real PID-1 reaper (tini) so the daemon the
> check starts is reaped after `stop` instead of lingering as a zombie.

## What the Go suite covers

`TestE2E_VersionAndHelp` (version/`--version`/help, daemon hidden) ·
`TestE2E_DoctorDoesNotTouchTheStore` (doctor text + `--json`; proves it does
**not** create/migrate `ao.db`) · `TestE2E_StatusStopped` (stopped + idempotent
stop) · `TestE2E_Lifecycle` (start, ready, idempotent, daemon-created store,
`/healthz` identity, stop, run-file cleanup) · `TestE2E_ShutdownGuard` (the
`/shutdown` CSRF + DNS-rebinding 403 guard, daemon survives) ·
`TestE2E_StaleRunFile` (dead-PID run-file → stale → cleaned) · `TestE2E_ExitCodes`
(2 usage / 1 runtime / config error) · `TestE2E_Completion` (all four shells).

## Why a Go suite (not bash, not Python)

The bash version grew past the point where bash was a good fit, and a Linux
container can't observe the macOS/Windows code paths at all. A Go `os/exec`
suite is the right home: it uses the repo's own toolchain (runs under `go test`),
gives real assertions and structured data, and — critically — runs natively on
the Windows and macOS runners, finally covering the `CREATE_NEW_PROCESS_GROUP`
detach path and per-OS config-dir resolution. The container stays as a thin
"clean install actually works" check.

## Extending

- **Add a case:** a new `TestE2E_*` function (or a `t.Run` subtest) in
  `e2e_test.go`. Use `newEnv(t)` for isolated state and the `env.run`/`httpGet`/
  `postShutdown` helpers.
- **Add an OS:** extend the `matrix.os` list in `.github/workflows/cli-e2e.yml`.
- Deeper per-OS path assertions (state resolves under the OS-native config dir
  when `AO_RUN_FILE`/`AO_DATA_DIR` are unset) fit best as unit tests in
  `internal/config`.
