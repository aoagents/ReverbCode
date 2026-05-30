// Package domain holds the shared contract types for the LCM + Session Manager
// lane: the canonical session state model, the derived display status, and the
// session read-model. It has no behaviour beyond pure derivation (status.go)
// and imports nothing outside the standard library, so every other package can
// depend on it without creating cycles.
package domain

import "time"

// LifecycleVersion is the schema version stamped onto every persisted record.
// Greenfield: we start at 1 and carry no migration/synthesis code.
const LifecycleVersion = 1

// CanonicalSessionLifecycle is the ONLY thing persisted for a session's state.
// The display status is derived from it on read (see DeriveLegacyStatus) and is
// never stored — this prevents canonical truth and display from drifting.
//
// Three orthogonal (state, reason) sub-states describe the session, its PR, and
// its runtime. Activity and Detecting are decider *inputs* that must survive
// between observations (they are read back by the pure decide core), so they
// live in the persisted record too.
type CanonicalSessionLifecycle struct {
	// Version is the Go-only schema-shape constant for this record. It is not
	// persisted and is not part of the CDC payload.
	Version int
	// Revision is the per-write monotonic counter. The storage layer's Upsert
	// bumps it when the full row is persisted; the LCM does not.
	Revision int             `json:"revision"`
	Session  SessionSubstate `json:"session"`
	PR       PRSubstate      `json:"pr"`
	Runtime  RuntimeSubstate `json:"runtime"`

	// Activity is the last-known agent activity. It arrives on a different
	// cadence (ApplyActivitySignal) than runtime probes (the reaper), so the
	// probe decider reads it from here to answer "was there recent activity?".
	Activity ActivitySubstate `json:"activity"`

	// Detecting is the anti-flap quarantine memory. It is non-nil only while
	// the session is in the detecting state; it carries the attempt counter,
	// the first-entry time, and a hash of the (timestamp-stripped) evidence so
	// the decider can tell "same ambiguous signal N times" from "signal moved".
	Detecting *DetectingState `json:"detecting,omitempty"`
}

// ---- session sub-state ----

type SessionState string

const (
	SessionNotStarted SessionState = "not_started"
	SessionWorking    SessionState = "working"
	SessionIdle       SessionState = "idle"
	SessionNeedsInput SessionState = "needs_input"
	SessionStuck      SessionState = "stuck"
	SessionDetecting  SessionState = "detecting"
	SessionDone       SessionState = "done"
	SessionTerminated SessionState = "terminated"
)

type SessionReason string

const (
	ReasonSpawnRequested          SessionReason = "spawn_requested"
	ReasonAgentAcknowledged       SessionReason = "agent_acknowledged"
	ReasonTaskInProgress          SessionReason = "task_in_progress"
	ReasonPRCreated               SessionReason = "pr_created"
	ReasonFixingCI                SessionReason = "fixing_ci"
	ReasonResolvingReviewComments SessionReason = "resolving_review_comments"
	ReasonAwaitingUserInput       SessionReason = "awaiting_user_input"
	ReasonAwaitingExternalReview  SessionReason = "awaiting_external_review"
	ReasonResearchComplete        SessionReason = "research_complete"
	ReasonMergedWaitingDecision   SessionReason = "merged_waiting_decision"
	ReasonManuallyKilled          SessionReason = "manually_killed"
	ReasonPRMerged                SessionReason = "pr_merged"
	ReasonAutoCleanup             SessionReason = "auto_cleanup"
	ReasonRuntimeLost             SessionReason = "runtime_lost"
	ReasonAgentProcessExited      SessionReason = "agent_process_exited"
	ReasonProbeFailure            SessionReason = "probe_failure"
	ReasonErrorInProcess          SessionReason = "error_in_process"
)

type SessionSubstate struct {
	State  SessionState  `json:"state"`
	Reason SessionReason `json:"reason"`
}

// ---- PR sub-state ----

type PRState string

const (
	PRNone   PRState = "none"
	PRDraft  PRState = "draft"
	PROpen   PRState = "open"
	PRMerged PRState = "merged"
	PRClosed PRState = "closed"
)

type PRReason string

const (
	PRReasonNotCreated       PRReason = "not_created"
	PRReasonInProgress       PRReason = "in_progress"
	PRReasonCIFailing        PRReason = "ci_failing"
	PRReasonReviewPending    PRReason = "review_pending"
	PRReasonChangesRequested PRReason = "changes_requested"
	PRReasonBotComments      PRReason = "bot_comments"
	PRReasonMergeConflicts   PRReason = "merge_conflicts"
	PRReasonApproved         PRReason = "approved"
	PRReasonMergeReady       PRReason = "merge_ready"
	PRReasonMerged           PRReason = "merged"
	PRReasonClosedUnmerged   PRReason = "closed_unmerged"
	PRReasonClearedOnRestore PRReason = "cleared_on_restore"
)

type PRSubstate struct {
	State  PRState  `json:"state"`
	Reason PRReason `json:"reason"`
	Number int      `json:"number,omitempty"`
	URL    string   `json:"url,omitempty"`
}

// ---- runtime sub-state ----

type RuntimeState string

const (
	RuntimeUnknown     RuntimeState = "unknown"
	RuntimeAlive       RuntimeState = "alive"
	RuntimeExited      RuntimeState = "exited"
	RuntimeMissing     RuntimeState = "missing"
	RuntimeProbeFailed RuntimeState = "probe_failed"
)

type RuntimeReason string

const (
	RuntimeReasonSpawnIncomplete     RuntimeReason = "spawn_incomplete"
	RuntimeReasonProcessRunning      RuntimeReason = "process_running"
	RuntimeReasonProcessMissing      RuntimeReason = "process_missing"
	RuntimeReasonTmuxMissing         RuntimeReason = "tmux_missing"
	RuntimeReasonManualKillRequested RuntimeReason = "manual_kill_requested"
	RuntimeReasonPRMergedCleanup     RuntimeReason = "pr_merged_cleanup"
	RuntimeReasonAutoCleanup         RuntimeReason = "auto_cleanup"
	RuntimeReasonProbeError          RuntimeReason = "probe_error"
)

type RuntimeSubstate struct {
	State  RuntimeState  `json:"state"`
	Reason RuntimeReason `json:"reason"`
}

// ---- activity sub-state (decider input) ----

type ActivityState string

const (
	ActivityActive       ActivityState = "active"
	ActivityReady        ActivityState = "ready"
	ActivityIdle         ActivityState = "idle"
	ActivityWaitingInput ActivityState = "waiting_input" // sticky: does not decay by wallclock
	ActivityBlocked      ActivityState = "blocked"       // sticky: does not decay by wallclock
	ActivityExited       ActivityState = "exited"
)

// IsSticky reports whether an activity state must NOT be aged/demoted by the
// passage of time (a paused agent is still paused until a new signal says so).
func (a ActivityState) IsSticky() bool {
	return a == ActivityWaitingInput || a == ActivityBlocked
}

type ActivitySource string

const (
	SourceNative   ActivitySource = "native"
	SourceTerminal ActivitySource = "terminal"
	SourceHook     ActivitySource = "hook"
	SourceRuntime  ActivitySource = "runtime"
	SourceNone     ActivitySource = "none"
)

type ActivitySubstate struct {
	State          ActivityState  `json:"state"`
	LastActivityAt time.Time      `json:"lastActivityAt"`
	Source         ActivitySource `json:"source"`
}

// ---- detecting quarantine memory (decider input) ----

type DetectingState struct {
	Attempts     int       `json:"attempts"`
	StartedAt    time.Time `json:"startedAt"`
	EvidenceHash string    `json:"evidenceHash"`
}
