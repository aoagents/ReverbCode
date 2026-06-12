package review

import (
	"context"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// agentRunner launches a reviewer agent one-shot over the worker's worktree by
// reusing the per-session agent resolver and runtime. The reviewer is not a
// session: its runtime pane is not persisted and not reaped here. It reviews the
// worktree and reports back by running `ao review submit`.
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

// reviewerEnv carries the worker the reviewer submits against, so the reviewer's
// `ao review submit` targets the right session.
func reviewerEnv(spec RunSpec) map[string]string {
	return map[string]string{"AO_REVIEW_WORKER": string(spec.WorkerID)}
}

func reviewPrompt(spec RunSpec) string {
	return fmt.Sprintf(`You are an AO code reviewer. Review the changes in this worktree for pull request %s.

Write your full review as Markdown to a file (for example review.md), then submit it by running:

  ao review submit %s --verdict <approved|changes_requested> --body review.md

Use changes_requested if the PR needs work, approved if it is ready. Do not push commits or modify the code — only review it.`, spec.PRURL, spec.WorkerID)
}
