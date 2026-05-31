package ports

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// SessionStore persists session records and serves the derived read-model's PR
// facts. The Session Manager creates rows; the Lifecycle Manager is the sole
// writer of canonical transitions thereafter.
type SessionStore interface {
	CreateSession(ctx context.Context, rec domain.SessionRecord) (domain.SessionRecord, error)
	UpdateSession(ctx context.Context, rec domain.SessionRecord) error
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
	ListSessions(ctx context.Context, project domain.ProjectID) ([]domain.SessionRecord, error)
	ListAllSessions(ctx context.Context) ([]domain.SessionRecord, error)
	// PRFactsForSession returns the PR facts that drive a session's display
	// status: the most-recently-updated non-closed PR, else the most recent.
	// Zero value (Exists=false) means the session has no PR.
	PRFactsForSession(ctx context.Context, id domain.SessionID) (domain.PRFacts, error)
}

// PRWriter records the PR facts a PR observation carries. The pr table's own DB
// triggers emit the CDC; this just writes the rows.
type PRWriter interface {
	// WritePR persists a full PR observation — scalar facts, check runs, and the
	// replacement comment set — in one transaction, so the rows and the CDC
	// events they emit are all-or-nothing.
	WritePR(ctx context.Context, pr PRRow, checks []PRCheckRow, comments []PRComment) error
	// RecentCheckStatuses reads the last `limit` runs of a check (the CI brake).
	RecentCheckStatuses(ctx context.Context, prURL, name string, limit int) ([]string, error)
}

// Notifier delivers an event to the human (desktop/Slack later). Push, never poll.
type Notifier interface {
	Notify(ctx context.Context, event Event) error
}

// AgentMessenger injects a message into a running agent (busy-detecting until the
// agent is ready). Used by the auto-nudge reactions.
type AgentMessenger interface {
	Send(ctx context.Context, id domain.SessionID, message string) error
}

type Priority string

const (
	PriorityUrgent Priority = "urgent"
	PriorityAction Priority = "action"
	PriorityInfo   Priority = "info"
)

// Event is a human-facing notification produced by a reaction.
type Event struct {
	Type      string
	Priority  Priority
	SessionID domain.SessionID
	ProjectID domain.ProjectID
	Message   string
}

// ---- runtime / agent / workspace plugin ports (used by the Session Manager) ----

type Runtime interface {
	Create(ctx context.Context, cfg RuntimeConfig) (RuntimeHandle, error)
	Destroy(ctx context.Context, handle RuntimeHandle) error
	IsAlive(ctx context.Context, handle RuntimeHandle) (bool, error)
}

type RuntimeConfig struct {
	SessionID     domain.SessionID
	WorkspacePath string
	LaunchCommand string
	Env           map[string]string
}

type RuntimeHandle struct {
	ID          string
	RuntimeName string
}

type Agent interface {
	GetLaunchCommand(cfg AgentConfig) string
	GetEnvironment(cfg AgentConfig) map[string]string
	GetRestoreCommand(agentSessionID string) string
}

type AgentConfig struct {
	SessionID     domain.SessionID
	WorkspacePath string
	Prompt        string
}

type Workspace interface {
	Create(ctx context.Context, cfg WorkspaceConfig) (WorkspaceInfo, error)
	Destroy(ctx context.Context, info WorkspaceInfo) error
	Restore(ctx context.Context, cfg WorkspaceConfig) (WorkspaceInfo, error)
}

type WorkspaceConfig struct {
	ProjectID domain.ProjectID
	SessionID domain.SessionID
	Branch    string
}

type WorkspaceInfo struct {
	Path      string
	Branch    string
	SessionID domain.SessionID
	ProjectID domain.ProjectID
}
