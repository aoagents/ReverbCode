// Package reviewrunner spawns a reviewer agent for a code review. It is kept out
// of the service layer (which stays thin and HTTP-facing) and sits beside the
// other orchestration packages such as session_manager: it owns the
// reviewer-resolver + runtime launch flow, not request handling.
package reviewrunner

import (
	"context"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	reviewsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/review"
)

// Runner spawns a reviewer over the worker's worktree, resolving the reviewer
// adapter from the reviewer registry (distinct from the worker agent set) and
// launching the command it returns on the runtime. It reuses the worker's
// worktree rather than cutting a second one: a fresh session worktree would
// branch off the project's default branch and so would not contain the worker's
// PR changes.
type Runner struct {
	reviewers ports.ReviewerResolver
	runtime   ports.Runtime
}

// New builds the production reviewer runner.
func New(reviewers ports.ReviewerResolver, runtime ports.Runtime) *Runner {
	return &Runner{reviewers: reviewers, runtime: runtime}
}

var _ reviewsvc.Runner = (*Runner)(nil)

// Run launches the reviewer for one review pass.
func (r *Runner) Run(ctx context.Context, spec reviewsvc.RunSpec) error {
	reviewer, ok := r.reviewers.Reviewer(spec.Harness)
	if !ok {
		return fmt.Errorf("no reviewer adapter for harness %q", spec.Harness)
	}
	reviewerID := "review-" + string(spec.WorkerID)
	cmd, err := reviewer.ReviewCommand(ctx, ports.ReviewInvocation{
		ReviewerID:      reviewerID,
		WorkerSessionID: spec.WorkerID,
		PRURL:           spec.PRURL,
		WorkspacePath:   spec.WorkspacePath,
	})
	if err != nil {
		return fmt.Errorf("reviewer command: %w", err)
	}
	if _, err := r.runtime.Create(ctx, ports.RuntimeConfig{
		SessionID:     domain.SessionID(reviewerID),
		WorkspacePath: spec.WorkspacePath,
		Argv:          cmd.Argv,
		Env:           reviewerEnv(spec, cmd.Env),
	}); err != nil {
		return fmt.Errorf("reviewer runtime: %w", err)
	}
	return nil
}

// reviewerEnv merges the adapter's env with AO_REVIEW_WORKER, which carries the
// worker the reviewer reports against so its `ao review submit` resolves the
// right worker session without a flag.
func reviewerEnv(spec reviewsvc.RunSpec, adapterEnv map[string]string) map[string]string {
	env := make(map[string]string, len(adapterEnv)+1)
	for k, v := range adapterEnv {
		env[k] = v
	}
	env["AO_REVIEW_WORKER"] = string(spec.WorkerID)
	return env
}
