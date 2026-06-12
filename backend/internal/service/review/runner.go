package review

import (
	"context"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// agentRunner spawns a reviewer agent over the worker's worktree, mirroring the
// session-manager launch flow (resolve agent by harness → build argv with its
// own prompt → runtime.Create). It reuses the worker's worktree rather than
// cutting a second one: a fresh session worktree would branch off the project's
// default branch and so would not contain the worker's PR changes. The reviewer
// reviews the code and posts its review to the PR itself.
type agentRunner struct {
	agents  ports.AgentResolver
	runtime ports.Runtime
}

// NewAgentRunner builds the production reviewer runner.
func NewAgentRunner(agents ports.AgentResolver, runtime ports.Runtime) Runner {
	return agentRunner{agents: agents, runtime: runtime}
}

func (r agentRunner) Run(ctx context.Context, spec RunSpec) error {
	agent, ok := r.agents.Agent(spec.Harness)
	if !ok {
		return fmt.Errorf("no agent adapter for reviewer harness %q", spec.Harness)
	}
	reviewerID := "review-" + string(spec.WorkerID)
	prompt := reviewPrompt(spec)
	argv, err := agent.GetLaunchCommand(ctx, ports.LaunchConfig{
		SessionID:     reviewerID,
		WorkspacePath: spec.WorkspacePath,
		Prompt:        prompt,
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
func reviewerEnv(spec RunSpec) map[string]string {
	return map[string]string{"AO_REVIEW_WORKER": string(spec.WorkerID)}
}

func reviewPrompt(spec RunSpec) string {
	return fmt.Sprintf(`You are an AO code reviewer. Review the changes in this worktree for pull request %s.

1. Post your review directly on the pull request on GitHub (use `+"`gh pr review`"+`): request changes if the PR needs work, approve if it is ready, and leave inline comments for specific findings.
2. Write your full review as Markdown to a file (for example review.md) and record the result with AO by running:

     ao review submit --verdict <approved|changes_requested> --body review.md

Do not push commits or modify the code — only review it.`, spec.PRURL)
}
