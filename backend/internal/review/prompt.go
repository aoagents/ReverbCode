package review

import "fmt"

// reviewTexts returns the user-facing prompt and the system prompt to deliver to
// a reviewer, authored in one place — the reviewer analogue of
// session_manager.buildSpawnTexts. The standing reviewer role lives in the
// system prompt; the per-pass task (which PR/commit, and the exact submit
// command carrying the ids) lives in the prompt, so it is also what AO injects
// into an already-running reviewer to review a new commit.
//
// The texts are self-contained — they carry the ids the reviewer needs to
// submit — so no environment variables are required.
func reviewTexts(spec LaunchSpec) (prompt, systemPrompt string) {
	systemPrompt = `## Code reviewer role

You are an AO code reviewer. You review a single pull request's changes in the current checkout — do not start unrelated work. Inspect what the PR changed by diffing the checkout against the PR's base branch, and review for correctness bugs, missing error handling, security issues, test coverage, and clear deviations from the surrounding code's conventions. Prefer a few high-confidence findings over nitpicks.

Post your review on the pull request using the available review tooling (request changes if it needs work, approve if it is ready), with inline comments for specific findings. Do not push commits, edit files, or modify the branch — review only.`

	prompt = fmt.Sprintf(`Review pull request %s (head commit %s).

Do these steps in order:
1. Post your review on the pull request and capture its id in one call. Post with `+"`gh api`"+` rather than `+"`gh pr review`"+`: it is the only way to attach inline comments, and its response carries the created review's id, so AO can tell the worker exactly which review to address. Send the review as a JSON body so the inline comments form a proper array of objects:

    gh api --method POST repos/{owner}/{repo}/pulls/{number}/reviews --input - --jq '.id' <<'JSON'
    { "event": "REQUEST_CHANGES", "body": "<summary>",
      "comments": [ { "path": "<file>", "line": <n>, "body": "<finding>" } ] }
    JSON

   - Substitute the PR's owner/repo/number. Add one object to "comments" per inline finding; omit the field for a review with no inline comments.
   - To approve, use "event": "APPROVE". GitHub does not let you approve a PR you opened — if that fails because you are the author, retry with "event": "COMMENT" and a body stating it is an approval.
   - The printed number is the review id. If the call fails on the provider, leave the id empty.
2. Record the result with AO. Write your full review to a temp file OUTSIDE the checkout — never into the worktree, or it gets committed onto the worker's branch — and pass that path to --body:

    f="$(mktemp)"; cat >"$f" <<'MD'
    <your full review markdown>
    MD
    ao review submit --session %s --run %s --verdict <approved|changes_requested> --body "$f" --review-id <id-from-step-1>

Only if step 1 genuinely fails on the provider, still run step 2 (without --review-id) so the result is recorded.`,
		spec.PRURL, spec.TargetSHA, spec.WorkerID, spec.RunID)
	return prompt, systemPrompt
}
