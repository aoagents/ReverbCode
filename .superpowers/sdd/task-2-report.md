# Task 2 Report: StashUncommitted + ApplyPreserved

## Status: DONE

## What was implemented

Two methods on the `gitworktree.Workspace` adapter, plus corresponding interface and test-double updates.

### StashUncommitted

Location: `backend/internal/adapters/workspace/gitworktree/workspace.go`

Algorithm:
1. `isDirty` early-exit for clean worktrees (returns `""`, nil).
2. Counts ignored-path skips via `git status --ignored --porcelain` and logs via `slog.InfoContext`.
3. Reserves a unique path via `os.CreateTemp` then immediately removes the 0-byte file so git sees an absent path (not a corrupt index). Deferred `os.Remove` cleans up after git writes it.
4. `GIT_INDEX_FILE=<tmppath> git -C <worktree> add -A` stages tracked edits and new non-ignored files, skipping .gitignore entries (no -f/--force).
5. `GIT_INDEX_FILE=<tmppath> git -C <worktree> write-tree` yields a tree SHA.
6. Compares tree SHA against `HEAD^{tree}`. If equal, only ignored files differ and nothing to preserve: returns `""`, nil.
7. `git -C <worktree> commit-tree <tree> [-p <HEAD>] -m "ao preserved <session-id>"` (omits -p on unborn HEAD).
8. `git -C <worktree> update-ref refs/ao/preserved/<session-id> <commit>` and returns the ref name.

### ApplyPreserved

1. Resolves the ref to a commit SHA via `rev-parse --verify`.
2. Runs `git checkout <SHA> -- .` which restores all files from the preserve tree onto the working tree without switching HEAD. Correctly reproduces tracked-file edits AND new untracked files that were captured by StashUncommitted.
3. On conflict (non-zero exit with "conflict" in output): returns `fmt.Errorf("%w: %w", ErrPreservedConflict, applyErr)`. Ref is NOT deleted.
4. On clean success: `git update-ref -d refs/ao/preserved/<session-id>`. Warn-logs if ref deletion fails (does not fail the call).

### Supporting additions

- `commands.go`: added `addAllTempIndexArgs`, `writeTreeArgs`, `commitTreeArgs`, `updateRefArgs`, `deleteRefArgs`, `revParseHeadArgs`, `checkoutTreeArgs`, `ignoredCountArgs`.
- `ports/outbound.go`: added `StashUncommitted` and `ApplyPreserved` to the `Workspace` interface.
- `ErrPreservedConflict` exported sentinel in `workspace.go`.
- Stub implementations added to `integration/lifecycle_sqlite_test.go` and `session_manager/manager_test.go`.

## TDD Evidence

### RED (tests written first, before any implementation)

```
$ go test ./internal/adapters/workspace/gitworktree/...
gitworktree [build failed]
  workspace_preserve_test.go:64:17: ws.StashUncommitted undefined (type *Workspace...)
  workspace_preserve_test.go:93:15: ws.ApplyPreserved undefined (type *Workspace...)
  workspace_preserve_test.go:142:17: ws.StashUncommitted undefined (type *Workspace...)
```

Tests failed because the methods did not exist yet. Confirmed they tested the right thing, not pre-existing behavior.

### GREEN (after implementation)

```
$ go test ./internal/adapters/workspace/gitworktree/... -v
Go test: 39 passed in 1 packages
```

All 39 tests pass, including:
- `TestWorkspaceIntegrationStashApplyRoundTrip`: full round-trip with tracked edit + new non-ignored file + .gitignore-d file. Asserts ref is non-empty, README edit reappears, agent-work.go reappears, secret.txt does NOT reappear, and ref is deleted after successful apply.
- `TestWorkspaceIntegrationStashCleanWorktree`: clean worktree returns empty ref.
- All 37 pre-existing tests unchanged.

### Build and vet

```
$ go build ./...     # Build: Success
$ go vet ./...       # Vet: No issues found
```

## Files Changed

- `backend/internal/adapters/workspace/gitworktree/workspace.go` (added StashUncommitted, ApplyPreserved, runCheckoutTree, countIgnoredPaths, ErrPreservedConflict, log/slog import)
- `backend/internal/adapters/workspace/gitworktree/commands.go` (added 8 new arg builders)
- `backend/internal/adapters/workspace/gitworktree/workspace_preserve_test.go` (new, 2 integration tests)
- `backend/internal/ports/outbound.go` (added 2 methods to Workspace interface)
- `backend/internal/integration/lifecycle_sqlite_test.go` (no-op stubs for new interface methods)
- `backend/internal/session_manager/manager_test.go` (no-op stubs for new interface methods)

## Commit

`cbe6f21 feat(workspace): add StashUncommitted and ApplyPreserved for session lifecycle`

## Self-review checklist

- [x] Preserve ref is exactly `refs/ao/preserved/<session-id>`
- [x] `.gitignore` respected: no `-f`/`--force` on `git add`, ignored count logged
- [x] Temp index file does NOT pre-exist when git writes it (0-byte file removed before git-add)
- [x] Working tree and global stash stack are never mutated during StashUncommitted
- [x] Clean worktree returns `""` and no error (two guards: isDirty + tree-SHA comparison)
- [x] Unborn HEAD handled: headSHA stays empty, commitTreeArgs omits `-p`
- [x] Ref deleted only after a confirmed clean apply
- [x] Ref kept on conflict; ErrPreservedConflict returned wrapped
- [x] No em dashes or en dashes anywhere in code, comments, or messages
- [x] App state stays under ~/.ao (temp index in os.TempDir, preserve refs in project .git)
- [x] All 39 tests pass, go vet clean, go build clean

## Concerns

One mild concern: `git checkout <SHA> -- .` also updates the staging area (index) in the re-added worktree, not just the working tree. This means after ApplyPreserved the restored files appear staged. For the session-lifecycle use case (agent resumes work) this is harmless and arguably desirable (the agent can commit immediately). If a future caller wants the files to appear as unstaged modifications, a `git reset HEAD` afterward would be needed. Current behavior matches the task requirements.

---

## Review Fix: Replace checkout with cherry-pick for correct conflict detection

### Status: DONE (review findings resolved)

### Problem found in review

`ApplyPreserved` used `git checkout <commitSHA> -- .`, which is a path-checkout (not a merge). Path-checkout unconditionally overwrites the working tree and always exits 0 for content divergence. This made the `if applyErr != nil` conflict-handling block unreachable dead code and left the spec requirement unmet: the `ErrPreservedConflict` sentinel could never be returned.

### New mechanism: cherry-pick --no-commit

Replaced with `git cherry-pick --no-commit <commitSHA>`. This computes the diff between the preserve commit and its parent (HEAD at save time) and performs a true three-way merge onto the current working tree. On conflict it leaves textual conflict markers (`<<<<<<<` etc.) in the affected files and exits non-zero WITHOUT committing or moving HEAD. Exit code alone drives conflict detection (locale-independent). New untracked files in the preserve tree come through as additions.

The `--no-commit` (`-n`) flag leaves no sequencer state that would require a follow-up `cherry-pick --abort`.

### Code changes

- `commands.go`: removed `checkoutTreeArgs` (path-checkout), added `cherryPickNoCommitArgs` (three-way merge).
- `workspace.go`: replaced `runCheckoutTree` with `runCherryPickNoCommit`; replaced string-scan conflict detection (`strings.Contains(out, "conflict")`) with exit-code detection (any non-zero exit from the merge step returns `ErrPreservedConflict` immediately).

### RED evidence

Under the OLD mechanism (`git checkout <SHA> -- .`), the new conflict test (`TestWorkspaceIntegrationApplyPreservedConflict`) would have failed because:

1. `git checkout <sha> -- .` exits 0 even when file content diverges (path-checkout never writes conflict markers and never signals a conflict).
2. `applyErr` would always be nil, so `ErrPreservedConflict` would never be returned.
3. The assertion `errors.Is(applyErr, ErrPreservedConflict)` would fail with "ApplyPreserved returned nil, want ErrPreservedConflict".

### GREEN evidence

```
$ /opt/homebrew/bin/go test -v ./internal/adapters/workspace/gitworktree/ \
    -run "TestWorkspaceIntegrationStashApplyRoundTrip|TestWorkspaceIntegrationApplyPreservedConflict" \
    -count=1

=== RUN   TestWorkspaceIntegrationStashApplyRoundTrip
2026/06/24 05:39:57 INFO gitworktree: StashUncommitted skipping ignored paths session=sess-preserve skipped_count=1
--- PASS: TestWorkspaceIntegrationStashApplyRoundTrip (0.47s)
=== RUN   TestWorkspaceIntegrationApplyPreservedConflict
2026/06/24 05:39:57 INFO gitworktree: StashUncommitted skipping ignored paths session=sess-conflict skipped_count=0
--- PASS: TestWorkspaceIntegrationApplyPreservedConflict (0.46s)
PASS
ok      github.com/aoagents/agent-orchestrator/backend/internal/adapters/workspace/gitworktree  1.072s
```

Full package: 40 tests pass (was 39 before this fix added the conflict test).

```
$ go build ./...   # Build: Success
$ go vet ./internal/adapters/workspace/gitworktree/...   # Vet: No issues found
```

### Files changed in this review fix

- `backend/internal/adapters/workspace/gitworktree/workspace.go` (ApplyPreserved + runCherryPickNoCommit replacing runCheckoutTree)
- `backend/internal/adapters/workspace/gitworktree/commands.go` (cherryPickNoCommitArgs replacing checkoutTreeArgs)
- `backend/internal/adapters/workspace/gitworktree/workspace_preserve_test.go` (added TestWorkspaceIntegrationApplyPreservedConflict)
