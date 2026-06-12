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
	}); err != nil {
		return fmt.Errorf("reviewer runtime: %w", err)
	}
	return nil
}

func reviewPrompt(spec RunSpec) string {
	return fmt.Sprintf(`You are an AO code reviewer. Review the changes in this worktree for pull request %s.

Post your review directly on the pull request on GitHub (use `+"`gh pr review`"+` or the GitHub CLI): request changes if the PR needs work, approve if it is ready, and leave inline comments for specific findings. Do not push commits or modify the code — only review it.`, spec.PRURL)
}
