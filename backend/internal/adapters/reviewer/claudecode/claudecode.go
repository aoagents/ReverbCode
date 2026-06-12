// Package claudecode is the claude-code reviewer adapter. claude-code is a
// prompt-driven agent, so this reviewer builds a review prompt and reuses the
// worker claude-code adapter's launch-command construction (binary resolution,
// flags). The reviewer contract itself stays prompt-agnostic, so a one-shot CLI
// reviewer (e.g. greptile) can implement it without a prompt.
package claudecode

import (
	"context"
	"fmt"

	workeragent "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/claudecode"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Reviewer is the claude-code code-review adapter.
type Reviewer struct {
	agent ports.Agent
}

// New builds the claude-code reviewer adapter.
func New() *Reviewer {
	return &Reviewer{agent: workeragent.New()}
}

// Harness identifies this reviewer in the reviewer registry.
func (r *Reviewer) Harness() domain.ReviewerHarness {
	return domain.ReviewerClaudeCode
}

var _ ports.Reviewer = (*Reviewer)(nil)

// ReviewCommand builds a one-shot claude-code invocation that reviews the
// worker's checkout for the PR, with the review prompt baked in.
func (r *Reviewer) ReviewCommand(ctx context.Context, inv ports.ReviewInvocation) (ports.ReviewCommandSpec, error) {
	argv, err := r.agent.GetLaunchCommand(ctx, ports.LaunchConfig{
		SessionID:     inv.ReviewerID,
		WorkspacePath: inv.WorkspacePath,
		Prompt:        reviewPrompt(inv),
	})
	if err != nil {
		return ports.ReviewCommandSpec{}, err
	}
	return ports.ReviewCommandSpec{Argv: argv}, nil
}

func reviewPrompt(inv ports.ReviewInvocation) string {
	return fmt.Sprintf(`You are an AO code reviewer. The current working directory is a checkout containing the changes for pull request %s. Review only this PR's changes — do not start unrelated work.

Steps:
1. Inspect what the PR changed by diffing the checkout against the PR's base branch.
2. Review for correctness bugs, missing error handling, security issues, test coverage, and clear deviations from the surrounding code's conventions. Prefer a few high-confidence findings over nitpicks.
3. Post your review on the pull request using the available review tooling (request changes if it needs work, approve if it is ready), with inline comments for specific findings.
4. Record the outcome with AO so the worker is nudged: write your full review to review.md, then run

     ao review submit --verdict <approved|changes_requested> --body review.md

Constraints: do not push commits, edit files, or modify the branch — review only. If you cannot post the review, still run `+"`ao review submit`"+` with your verdict and findings so the result is recorded.`, inv.PRURL)
}
