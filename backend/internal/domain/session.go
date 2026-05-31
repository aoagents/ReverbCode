package domain

import "time"

// SessionID, ProjectID, IssueID are distinct string types so they can't be
// swapped at a call site by accident.
type (
	SessionID string
	ProjectID string
	IssueID   string
)

type SessionKind string

const (
	KindWorker       SessionKind = "worker"
	KindOrchestrator SessionKind = "orchestrator"
)

// SessionMetadata is the typed, off-canonical metadata for a session: the
// operational handles and seed inputs the Session Manager and reaper need but
// that are NOT part of the canonical lifecycle. The set of fields is fixed here
// (no free-form keys), so what a session can carry is a compile-time fact, and
// it is folded into the sessions row off the CDC path.
//
// Empty fields mean "unset": the LCM merges metadata without overwriting a
// stored value with an empty one, so a partial write (spawn setting only the
// runtime handle) does not clobber a value set earlier (the branch at creation).
type SessionMetadata struct {
	Branch          string `json:"branch,omitempty"`
	WorkspacePath   string `json:"workspacePath,omitempty"`
	RuntimeHandleID string `json:"runtimeHandleId,omitempty"`
	RuntimeName     string `json:"runtimeName,omitempty"`
	AgentSessionID  string `json:"agentSessionId,omitempty"`
	Prompt          string `json:"prompt,omitempty"`
}

// IsZero reports whether no metadata field is set.
func (m SessionMetadata) IsZero() bool { return m == SessionMetadata{} }

// SessionRecord is the PERSISTENCE shape: identity, canonical lifecycle, and
// metadata — everything the store holds, and nothing derived. The store reads
// and writes records; it never produces the derived display status.
//
// Metadata is json:"-" on purpose: it lives off the canonical path, so it must
// never ride along in the change_log / snapshot payloads. Enforcing that at the
// type level means no caller has to remember to scrub it before marshalling.
type SessionRecord struct {
	ID        SessionID                 `json:"id"`
	ProjectID ProjectID                 `json:"projectId"`
	IssueID   IssueID                   `json:"issueId,omitempty"`
	Kind      SessionKind               `json:"kind"`
	Lifecycle CanonicalSessionLifecycle `json:"lifecycle"`
	Metadata  SessionMetadata           `json:"-"`
	CreatedAt time.Time                 `json:"createdAt"`
	UpdatedAt time.Time                 `json:"updatedAt"`
}

// Session is the read-model returned across the API boundary (to controllers,
// then the frontend): a SessionRecord plus the DERIVED display Status. The
// Session Manager is the single producer of Status — it builds a Session from a
// stored SessionRecord by calling DeriveLegacyStatus, so the store and API
// never recompute (or accidentally persist) it.
type Session struct {
	SessionRecord
	Status SessionStatus `json:"status"`
}
