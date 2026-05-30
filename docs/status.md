# LCM + Session Manager — status & roadmap

Where the lane stands, what's left, and where to plug in.

## Branch model

`feat/lcm-sm-contracts` is the **lane integration branch**: each sub-PR below
branched off it and merged **into** it. The whole lane lands on `main` as one
unit once it's ready. Sub-PRs were reviewed against the integration branch;
the eventual lane→main merge is a single cumulative review.

## Done — implementation complete (behind fakes)

| Area | What landed | PR |
|------|-------------|----|
| Skeleton | `backend/` (Go) + `frontend/` (Electron/TS) | #1 (on `main`) |
| Contracts + CI | `domain/` + `ports/`; Go + gitleaks workflows | #2 |
| Pure DECIDE core | the deciders + anti-flap quarantine + exhaustive truth-table tests | #4 |
| LCM — pipeline | `Apply*` pipeline, per-session serialization, store integration, composition rules, detecting-memory lifecycle | #5 |
| LCM — reactions | reaction table + escalation engine + real `TickEscalations` | #6 |
| Session Manager | spawn / kill / restore / cleanup / list, eager rollback, worktree-remove safety | #7 |

`gofmt` / `go build` / `go vet` / `go test -race` all green across `domain`,
`domain/decide`, `lifecycle`, and `session`. The `decide` core is at 100%
statement coverage; the impl packages cover the load-bearing logic including the
error/rollback paths.

### Build & test

```
cd backend
gofmt -l .          # must print nothing
go build ./...
go vet ./...
go test -race ./...
go test -cover ./...
```

## Not done — the integration phase

Everything above runs against **in-memory fakes**. Making it a live system means
swapping fakes for real adapters (built by other lanes) behind the existing
ports, and resolving the carried-forward items below.

### Carried-forward items (must be addressed as real adapters land)

- **`react()` out-of-lock dispatch.** Reactions fire after the per-session lock
  releases (deliberate, so a busy-waiting send-to-agent doesn't hold the mutex).
  Under a live daemon with concurrent observers this can dispatch on a stale
  snapshot / out of order. Give `react()` a per-session ordering (a small react
  queue) or re-check the triggering state before dispatching. Documented in
  `lifecycle/reactions.go`.
- **`ExpectedRevision` optimistic-concurrency is unused.** The in-process
  per-session mutex covers a single daemon. Multi-writer or CDC-driven setups
  must use the `LifecyclePatch.ExpectedRevision` CAS the contract already exposes.
- **Store `Seed` + `Get` need a real implementation.** The Session Manager added
  two record-with-identity methods to `LifecycleStore`; the real persistence
  layer must implement them (create-with-identity that rejects an existing id;
  full-record read by id). Documented in `ports/outbound.go`.

### Real adapters needed (other lanes)

| Port | Real adapter | Owning lane |
|------|--------------|-------------|
| `LifecycleStore` | target SQLite persistence layer (WAL + atomic state, outbox, and change log writes + CDC) | persistence |
| `SCMFacts` producer | SCM poller (batch PR/CI/review enrichment) | SCM |
| `Runtime` / `Agent` / `Workspace` | tmux runtime, claude-code/codex agent, git-worktree workspace | coding-agents |
| `Notifier` | desktop/Slack notifier | notifications |
| `AgentMessenger` | tmux inject with busy-detect + delivery verify | coding-agents |
| `SessionManager` consumer | backend API (routes/controllers) + OpenAPI | API |

### Open cross-lane contract questions

- **SCM facts** — does `SCMFacts` match what the poller can cheaply produce
  (batch enrichment, CI log tail as a pointer)?
- **Persistence** — is `LifecycleStore` + `LifecyclePatch` the right boundary?
  Per-session lock vs. the `ExpectedRevision` CAS?
- **API** — is the `SessionManager` interface + the `Session` read-model
  OpenAPI-friendly?

### Land the lane → `main`

A final cumulative review of `feat/lcm-sm-contracts` vs. `main`, then merge the
complete lane in one unit.

## Where to plug in (for someone picking this up)

- **Implementing a real adapter?** Write it to satisfy the matching interface in
  `ports/`, then construct the `lifecycle.Manager` / `session.Manager` with it in
  place of the fake. Nothing in `domain`/`lifecycle`/`session` should need to
  change.
- **Changing decision behavior?** It lives in `domain/decide` (pure) — add a
  truth-table case first; nothing there does I/O.
- **Adding a reaction?** Extend the table in `lifecycle/reactions.go` and map the
  triggering status in `reactionEventFor`.
- **Don't** persist the display status, conclude death outside the probe
  pipeline, or `rm -rf` a still-registered worktree — see the invariants in
  [architecture.md](architecture.md#7-load-bearing-invariants).
