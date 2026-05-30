package ports

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// LifecycleStore is Tom's persistence adapter for session records.
//
// Writer contract: the Lifecycle Manager (LCM) is the sole logical writer of
// sessions. Controllers, the Session Manager, observers, and other goroutines
// must route mutations to the LCM; no other goroutine writes sessions directly.
// The LCM serializes mutations and calls Upsert with the full SessionRecord and
// the classified event_type. The storage layer owns Revision++ and performs the
// full-row insert-or-update; the older sparse merge-patch model is gone.
//
// List/Get return persistence records (no derived status); the Session Manager
// hydrates them into domain.Session by attaching DeriveLegacyStatus on read.
type LifecycleStore interface {
	// Upsert inserts or replaces the full session row and bumps Revision inside
	// the storage layer. Only the LCM may call it.
	Upsert(ctx context.Context, rec domain.SessionRecord, eventType EventType) error
	Load(ctx context.Context, id domain.SessionID) (domain.CanonicalSessionLifecycle, bool, error)
	List(ctx context.Context, project domain.ProjectID) ([]domain.SessionRecord, error)
	GetMetadata(ctx context.Context, id domain.SessionID) (map[string]string, error)
	PatchMetadata(ctx context.Context, id domain.SessionID, kv map[string]string) error

	// Get returns a single full record (with identity) by id. Load is
	// lifecycle-only, so readers use this to build the read-model and reconstruct
	// teardown handles for Kill/Restore on one id.
	Get(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
}

// EventType is the schema-level event label attached to each Upsert.
type EventType string

const (
	EventSessionCreated          EventType = "session_created"
	EventSessionTerminated       EventType = "session_terminated"
	EventSessionStateChanged     EventType = "session_state_changed"
	EventSessionPRUpdated        EventType = "session_pr_updated"
	EventSessionRuntimeUpdated   EventType = "session_runtime_updated"
	EventSessionAttentionUpdated EventType = "session_attention_updated"
	EventSessionActivityUpdated  EventType = "session_activity_updated"
	EventSessionDisplayUpdated   EventType = "session_display_updated"
	EventSessionUpdated          EventType = "session_updated"
)

// Notifier delivers events to the human (desktop/Slack later). Push, never pull.
type Notifier interface {
	Notify(ctx context.Context, event OrchestratorEvent) error
}

type EventPriority string

const (
	PriorityUrgent  EventPriority = "urgent"
	PriorityAction  EventPriority = "action"
	PriorityWarning EventPriority = "warning"
	PriorityInfo    EventPriority = "info"
)

type OrchestratorEvent struct {
	Type      string
	Priority  EventPriority
	SessionID domain.SessionID
	ProjectID domain.ProjectID
	Message   string
	Data      map[string]any
}

// AgentMessenger injects a message into a running agent. The implementation
// busy-detects (waits for the agent to be idle/ready) and verifies delivery,
// which is why activity-detection accuracy matters.
type AgentMessenger interface {
	Send(ctx context.Context, id domain.SessionID, message string) error
}

// The runtime/agent/workspace plugin ports are co-owned with the coding-agents
// lane; the method sets below are the minimum the Session Manager spawn/kill
// pipelines call. They will be fleshed out alongside the tmux/claude-code impls.

type Runtime interface {
	Create(ctx context.Context, cfg RuntimeConfig) (RuntimeHandle, error)
	Destroy(ctx context.Context, handle RuntimeHandle) error
	SendMessage(ctx context.Context, handle RuntimeHandle, message string) error
	GetOutput(ctx context.Context, handle RuntimeHandle, lines int) (string, error)
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
	// ProbeProcess returns the agent process liveness classification
	// (alive/dead/indeterminate/failed) — not a boolean and not an activity
	// state. Activity classification arrives separately via ActivitySignal.
	ProbeProcess(ctx context.Context, handle RuntimeHandle) (ProcessProbe, error)
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
	List(ctx context.Context, project domain.ProjectID) ([]WorkspaceInfo, error)
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
