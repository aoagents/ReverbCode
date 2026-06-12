// Package reviewrunner spawns a reviewer agent for a code review. It is kept out
// of the service layer (which stays thin and HTTP-facing) and sits beside the
// other orchestration packages such as session_manager: it owns the
// agent-resolver + runtime launch flow, not request handling.
package reviewrunner

import (
	"context"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	reviewsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/review"
)

// Runner spawns a reviewer agent over the worker's worktree, mirroring the
// session-manager launch flow (resolve agent by harness → build argv with its
// own prompt → runtime.Create). It reuses the worker's worktree rather than
// cutting a second one: a fresh session worktree would branch off the project's
// default branch and so would not contain the worker's PR changes. The reviewer
// reviews the code and posts its review to the PR itself.
type Runner struct {
	agents  ports.AgentResolver
	runtime ports.Runtime
}

// New builds the production reviewer runner.
func New(agents ports.AgentResolver, runtime ports.Runtime) *Runner {
	return &Runner{agents: agents, runtime: runtime}
}

var _ reviewsvc.Runner = (*Runner)(nil)

// Run launches the reviewer agent for one review pass.
func (r *Runner) Run(ctx context.Context, spec reviewsvc.RunSpec) error {
	agent, ok := r.agents.Agent(spec.Harness)
	if !ok {
		return fmt.Errorf("no agent adapter for reviewer harness %q", spec.Harness)
	}
	reviewerID := "review-" + string(spec.WorkerID)
	argv, err := agent.GetLaunchCommand(ctx, ports.LaunchConfig{
		SessionID:     reviewerID,
		WorkspacePath: spec.WorkspacePath,
		Prompt:        reviewPrompt(spec),
	})
	if err != nil {
		return fmt.Errorf("reviewer launch command: %w", err)
	}
	if _, err := r.runtime.Create(ctx, ports.RuntimeConfig{
		SessionID:     domain.SessionID(reviewerID),
		WorkspacePath: spec.WorkspacePath,
		Argv:          argv,
		Env:           reviewerEnv(spec),
	}); err != nil {
		return fmt.Errorf("reviewer runtime: %w", err)
	}
	return nil
}

// reviewerEnv carries the worker the reviewer reports against, so the reviewer's
// `ao review submit` resolves the right worker session without a flag.
func reviewerEnv(spec reviewsvc.RunSpec) map[string]string {
	return map[string]string{"AO_REVIEW_WORKER": string(spec.WorkerID)}
}

func reviewPrompt(spec reviewsvc.RunSpec) string {
	return fmt.Sprintf(`You are an AO code reviewer. The current working directory is a checkout containing the changes for pull request %s. Review only this PR's changes — do not start unrelated work.

Steps:
1. Inspect what the PR changed by diffing the checkout against the PR's base branch.
2. Review for correctness bugs, missing error handling, security issues, test coverage, and clear deviations from the surrounding code's conventions. Prefer a few high-confidence findings over nitpicks.
3. Post your review on the pull request using whatever review tooling is available for this provider (request changes if it needs work, approve if it is ready), with inline comments for specific findings.
4. Record the outcome with AO so the worker is nudged: write your full review to review.md, then run

     ao review submit --verdict <approved|changes_requested> --body review.md

Constraints: do not push commits, edit files, or modify the branch — review only. If you cannot post the review on the provider, still run `+"`ao review submit`"+` with your verdict and findings so the result is recorded.`, spec.PRURL)
}
