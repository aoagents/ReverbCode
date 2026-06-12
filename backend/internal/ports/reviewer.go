package ports

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Reviewer is the contract a code-review adapter satisfies. It is deliberately
// separate from Agent: a reviewer is invoked once over a checkout to review a
// PR, and need not be a prompt-fed interactive agent. A prompt-driven reviewer
// (claude-code) builds its own prompt internally; a one-shot CLI (greptile)
// returns its own argv with no prompt at all.
type Reviewer interface {
	// ReviewCommand builds the command (and any extra env) AO should run to
	// review the worker's checkout for a PR.
	ReviewCommand(ctx context.Context, inv ReviewInvocation) (ReviewCommandSpec, error)
}

// ReviewInvocation describes one review pass for a reviewer to act on.
type ReviewInvocation struct {
	// ReviewerID is a stable id for the reviewer's runtime instance (pane,
	// native session id), derived from the worker session.
	ReviewerID string
	// WorkerSessionID is the worker whose PR is under review.
	WorkerSessionID domain.SessionID
	// PRURL is the pull request to review.
	PRURL string
	// WorkspacePath is the worker's checkout the reviewer reads.
	WorkspacePath string
}

// ReviewCommandSpec is how to launch a reviewer: the argv and any extra env the
// adapter needs. AO supplies the workspace and review-tracking env around it.
type ReviewCommandSpec struct {
	Argv []string
	Env  map[string]string
}

// ReviewerResolver maps a reviewer harness onto its adapter. ok=false means no
// adapter is registered for that harness.
type ReviewerResolver interface {
	Reviewer(harness domain.ReviewerHarness) (Reviewer, bool)
}
