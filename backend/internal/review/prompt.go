package review

import "fmt"

// reviewPrompt is the review instruction AO gives every reviewer, authored in
// one place (the reviewer analogue of session_manager's worker prompt) rather
// than per-adapter. Prompt-driven adapters feed it to their agent; one-shot CLI
// reviewers may ignore it. It is self-contained — it carries the ids the
// reviewer needs to submit, so no environment variables are required.
func reviewPrompt(spec LaunchSpec) string {
	return fmt.Sprintf(`You are an AO code reviewer. The current working directory is a checkout containing the changes for pull request %s (head commit %s). Review only this PR's changes — do not start unrelated work.

Steps:
1. Inspect what the PR changed by diffing the checkout against the PR's base branch.
2. Review for correctness bugs, missing error handling, security issues, test coverage, and clear deviations from the surrounding code's conventions. Prefer a few high-confidence findings over nitpicks.
3. Post your review on the pull request using the available review tooling (request changes if it needs work, approve if it is ready), with inline comments for specific findings.
4. Record the outcome with AO so the worker is nudged: write your full review to review.md, then run exactly:

     ao review submit --session %s --run %s --verdict <approved|changes_requested> --body review.md

Constraints: do not push commits, edit files, or modify the branch — review only. If you cannot post the review, still run the `+"`ao review submit`"+` command above so the result is recorded.`,
		spec.PRURL, spec.TargetSHA, spec.WorkerID, spec.RunID)
}
