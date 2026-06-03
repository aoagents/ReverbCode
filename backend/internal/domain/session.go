package domain

import "time"

// These ID types are distinct string types so they can't be swapped at a call
// site by accident.
type (
	// SessionID identifies a session.
	SessionID string
	// ProjectID identifies a project.
	ProjectID string
	// IssueID identifies a tracker issue.
	IssueID string
)

// SessionKind distinguishes a worker session from an orchestrator session.
type SessionKind string

// Session kinds.
const (
	KindWorker       SessionKind = "worker"
	KindOrchestrator SessionKind = "orchestrator"
)

// SessionMetadata is the typed, off-status metadata for a session: operational
// handles and seed inputs used by Session Manager and reaper.
type SessionMetadata struct {
	Branch          string `json:"branch,omitempty"`
	WorkspacePath   string `json:"workspacePath,omitempty"`
	RuntimeHandleID string `json:"runtimeHandleId,omitempty"`
	AgentSessionID  string `json:"agentSessionId,omitempty"`
	Prompt          string `json:"prompt,omitempty"`
}

// SessionRecord is the persistence shape. It intentionally stores only durable
// facts: identity, agent harness, activity_state, is_terminated, and operational
// metadata. The user-facing Status is derived from these facts plus PR facts.
type SessionRecord struct {
	ID           SessionID       `json:"id"`
	ProjectID    ProjectID       `json:"projectId"`
	IssueID      IssueID         `json:"issueId,omitempty"`
	Kind         SessionKind     `json:"kind"`
	Harness      AgentHarness    `json:"harness,omitempty"`
	DisplayName  string          `json:"displayName,omitempty"`
	Activity     Activity        `json:"activity"`
	IsTerminated bool            `json:"isTerminated"`
	Metadata     SessionMetadata `json:"-"`
	CreatedAt    time.Time       `json:"createdAt"`
	UpdatedAt    time.Time       `json:"updatedAt"`
}

// Session is the read-model returned across the API boundary: a SessionRecord
// plus the derived display Status.
type Session struct {
	SessionRecord
	Status SessionStatus `json:"status"`
}
