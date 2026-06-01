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

// SpawnOutcome is what the Session Manager reports once a spawn is live: the
// handles needed for later teardown/restore.
type SpawnOutcome struct {
	Branch         string
	WorkspacePath  string
	RuntimeHandle  RuntimeHandle
	AgentSessionID string
	Prompt         string
}
