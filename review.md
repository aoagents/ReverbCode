# Review: fix(review): notify worker on changes_requested instead of relying on SCM poll (#337)

**Verdict: changes_requested**

The core change is sound and well-built. On a `changes_requested` verdict, `Engine.Submit` now nudges the worker's live pane directly through `ports.AgentMessenger.Send` instead of relying on the SCM poll loop (which never observes `CHANGES_REQUESTED` for self/COMMENT-state reviews). The new `github_review_id` round-trip (CLI flag → service → engine → store column + worker message) is plumbed cleanly end-to-end, the migration is correctly numbered 0016 (no collision with the renumbered 0015 telemetry migration), sqlc gen is in sync, and the unit tests are thorough (worker messaged with body+id, approved does not message, id-omission path, messenger error surfaced). `go build`, `go vet`, `gofmt`, and the affected package tests are all clean locally.

One issue should be fixed before merge.

## Findings

### 1. (blocking) The documented `gh api` command will not attach inline comments

`internal/review/prompt.go` (the reviewer-agent prompt) now instructs:

```
gh api --method POST repos/{owner}/{repo}/pulls/{number}/reviews \
  -f event=REQUEST_CHANGES -f body="<summary>" \
  -f 'comments[][path]=<file>' -F 'comments[][line]=<n>' -f 'comments[][body]=<finding>' \
  --jq '.id'
```

`gh api`'s `-f`/`-F` field parser builds a flat request body. It only treats a key **ending in `[]`** as "append a scalar to an array" — it has no support for constructing an array of objects. A key like `comments[][path]` is taken as a literal top-level field name, so this produces a body like `{"comments[][path]": "...", "comments[][line]": 1, ...}` rather than `{"comments": [{"path": ..., "line": ..., "body": ...}]}`. GitHub's create-review endpoint will reject that (422 on the unexpected fields) or drop the comments.

This matters because attaching inline comments via `gh api` is the stated reason for switching away from `gh pr review` in the latest two commits. As written, the headline mechanism won't work: the reviewer agent will either fail the POST (and fall back to the no-`--review-id` path, losing both the id and the inline comments) or post a review with no inline findings.

The reliable way to send an array-of-objects body is `--input` with a JSON document, e.g.:

```
gh api --method POST repos/{owner}/{repo}/pulls/{number}/reviews --input - --jq '.id' <<'JSON'
{ "event": "REQUEST_CHANGES",
  "body": "<summary>",
  "comments": [ { "path": "<file>", "line": <n>, "body": "<finding>" } ] }
JSON
```

Please update the prompt to use an `--input` JSON body (or otherwise a form `gh api` can actually serialize) for the inline-comments case. The id capture (`--jq '.id'`) and the approve/COMMENT fallback prose are fine as-is.

## Non-blocking notes

- **Submit error after DB commit.** If `messenger.Send` fails, `Submit` returns an error _after_ `UpdateReviewRunResult` has already persisted the verdict as `complete`. A retried `ao review submit` then fails with "review run is not running" (the update is gated on `status='running'`), so a transient pane-send failure leaves the run recorded but the worker un-nudged with no clean retry. This is explicitly called out as deferred (resiliency / double-submit idempotency) in the PR description, so it's acceptable for this scope — flagging only so it's tracked.

Everything else looks good to merge once the `gh api` command is corrected.
