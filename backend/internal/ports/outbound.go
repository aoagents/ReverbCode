package ports

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// PRWriter records the PR facts a PR observation carries. The pr table's own DB
// triggers emit the CDC; this just writes the rows.
type PRWriter interface {
	// WritePR persists a full PR observation — scalar facts, check runs, and the
	// replacement comment set — in one transaction, so the rows and the CDC
	// events they emit are all-or-nothing.
	WritePR(ctx context.Context, pr domain.PRRow, checks []domain.PRCheckRow, comments []domain.PRComment) error
}

// AgentMessenger injects a message into a running agent.
type AgentMessenger interface {
	Send(ctx context.Context, id domain.SessionID, message string) error
}

// ---- runtime / agent / workspace plugin ports (used by the Session Manager) ----

// Runtime is where a session's agent process runs — currently a Zellij session.
// The Session Manager creates one per session and tears it down.
type Runtime interface {
	Create(ctx context.Context, cfg RuntimeConfig) (RuntimeHandle, error)
	Destroy(ctx context.Context, handle RuntimeHandle) error
	IsAlive(ctx context.Context, handle RuntimeHandle) (bool, error)
}

// RuntimeConfig is the spec for launching a session's process in a Runtime.
type RuntimeConfig struct {
	SessionID     domain.SessionID
	WorkspacePath string
	LaunchCommand string
	Env           map[string]string
}

// RuntimeHandle identifies a live runtime instance (a Zellij session/pane handle).
type RuntimeHandle struct {
	ID string
}

// Agent is the AI coding tool driving a session (claude-code, codex, …): it
// supplies the launch/restore commands and the process environment.
type Agent interface {
	GetLaunchCommand(cfg AgentConfig) string
	GetEnvironment(cfg AgentConfig) map[string]string
	GetRestoreCommand(agentSessionID string) string
}

// AgentConfig is the per-session input to an Agent's command and environment.
type AgentConfig struct {
	SessionID     domain.SessionID
	WorkspacePath string
	Prompt        string
}

// Workspace is the isolated checkout an agent works in (a git worktree or clone).
type Workspace interface {
	Create(ctx context.Context, cfg WorkspaceConfig) (WorkspaceInfo, error)
	Destroy(ctx context.Context, info WorkspaceInfo) error
	Restore(ctx context.Context, cfg WorkspaceConfig) (WorkspaceInfo, error)
}

// WorkspaceConfig is the spec for creating or restoring a session's workspace.
type WorkspaceConfig struct {
	ProjectID domain.ProjectID
	SessionID domain.SessionID
	Branch    string
}

// WorkspaceInfo describes a created workspace — where it lives and its branch.
type WorkspaceInfo struct {
	Path      string
	Branch    string
	SessionID domain.SessionID
	ProjectID domain.ProjectID
}
