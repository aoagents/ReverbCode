package ports

import "github.com/aoagents/agent-orchestrator/backend/internal/domain"

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
