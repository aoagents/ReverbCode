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
1. Post your review on the pull request with `+"`gh`"+`, with inline comments for specific findings:
   - If changes are needed, request changes.
   - If it is ready, approve it. GitHub does not let you approve a PR you opened — if the approval is rejected because you are the PR author, post the same review as a regular comment instead (a COMMENT-event review whose body states it is an approval).
2. Write your full review to review.md and record the result with AO by running exactly:

    ao review submit --session %s --run %s --verdict <approved|changes_requested> --body review.md

Only if step 1 genuinely fails on the provider, still run step 2 so the result is recorded.`,
		spec.PRURL, spec.TargetSHA, spec.WorkerID, spec.RunID)
	return prompt, systemPrompt
}
