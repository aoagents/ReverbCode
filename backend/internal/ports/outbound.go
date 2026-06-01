package ports

import (
	"context"
	"time"

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
	WritePR(ctx context.Context, pr domain.PRRow, checks []domain.PRCheckRow, comments []domain.PRComment) error
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

// Priority ranks a notification's urgency so a notifier can decide how loudly
// to surface it, from PriorityUrgent down to PriorityInfo.
type Priority string

// Notification priorities, highest urgency first.
const (
	PriorityUrgent  Priority = "urgent"
	PriorityAction  Priority = "action"
	PriorityWarning Priority = "warning"
	PriorityInfo    Priority = "info"
)

// Event is a human-facing notification produced by a reaction. It carries the
// stable reaction/escalation context a durable notification renderer needs,
// while lifecycle remains responsible for deciding what should notify.
type Event struct {
	Type       string
	Priority   Priority
	SessionID  domain.SessionID
	ProjectID  domain.ProjectID
	Message    string
	Reaction   *ReactionEvent
	Escalation *EscalationEvent
	DedupeKey  string
	CauseKey   string
	OccurredAt time.Time
}

// ReactionEvent is the reaction context carried on an Event: which reaction
// fired and whether it merely notified or escalated.
type ReactionEvent struct {
	Key    string // agent-needs-input, approved-and-green, ci-failed, etc.
	Action string // notify | escalated
}

// EscalationEvent is the escalation context carried on an Event once a reaction
// has exhausted its retry/attempt/duration budget.
type EscalationEvent struct {
	Attempts   int
	Cause      string // max_retries | max_attempts | max_duration
	DurationMs int64
}

// ---- runtime / agent / workspace plugin ports (used by the Session Manager) ----

// Runtime is where a session's agent process runs — a tmux/zellij session or a
// bare process. The Session Manager creates one per session and tears it down.
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

// RuntimeHandle identifies a live runtime instance (e.g. a tmux session).
type RuntimeHandle struct {
	ID          string
	RuntimeName string
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
