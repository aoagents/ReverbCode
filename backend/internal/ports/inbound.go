package ports

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

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
