package ports

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// LifecycleManager is the inbound contract for durable session lifecycle facts
// and lifecycle-owned agent nudges. PR row writes live in the PR service; PR
// observations are passed here after persistence so lifecycle can prompt agents
// to fix CI, review comments, or merge conflicts.
type LifecycleManager interface {
	ApplyRuntimeObservation(ctx context.Context, id domain.SessionID, f RuntimeFacts) error
	ApplyActivitySignal(ctx context.Context, id domain.SessionID, s ActivitySignal) error
	ApplyPRObservation(ctx context.Context, id domain.SessionID, o PRObservation) error

	// MarkSpawned marks a session live and records its handles. It works for a
	// fresh spawn and a restore.
	MarkSpawned(ctx context.Context, id domain.SessionID, o SpawnOutcome) error
	MarkTerminated(ctx context.Context, id domain.SessionID) error
}

// SessionManager is the inbound contract the API/CLI call for explicit
// mutations. It drives the runtime/agent/workspace plugins and routes durable
// session-fact writes to the LCM.
type SessionManager interface {
	Spawn(ctx context.Context, cfg SpawnConfig) (domain.Session, error)
	Kill(ctx context.Context, id domain.SessionID) (freed bool, err error)
	Restore(ctx context.Context, id domain.SessionID) (domain.Session, error)
	List(ctx context.Context, project domain.ProjectID) ([]domain.Session, error)
	Get(ctx context.Context, id domain.SessionID) (domain.Session, error)
	Send(ctx context.Context, id domain.SessionID, message string) error
	Cleanup(ctx context.Context, project domain.ProjectID) ([]domain.SessionID, error)
}

// SpawnConfig is the request to start a new session: which project/issue, which
// agent harness, and the branch/prompt/rules the agent launches with.
type SpawnConfig struct {
	ProjectID  domain.ProjectID
	IssueID    domain.IssueID
	Kind       domain.SessionKind
	Harness    domain.AgentHarness
	Branch     string
	Prompt     string
	AgentRules string
}
