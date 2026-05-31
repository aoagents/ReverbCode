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

// CanonicalSessionLifecycle is the ONLY lifecycle state persisted for a session.
// The display status is derived from it (plus the session's PR facts, which live
// in the separate pr table) on read — see DeriveStatus — and is never stored, so
// canonical truth and display cannot drift.
//
// PR facts are deliberately NOT here: a session can own several PRs over its
// life, and PR state is owned by the pr table. The runtime axis is collapsed to
// a single IsAlive boolean. Activity and Detecting are decider *inputs* that
// must survive between observations, so they live in the persisted record.
type CanonicalSessionLifecycle struct {
	// Version is the Go-only schema-shape constant for this record. It is not
	// persisted and is not part of the CDC payload.
	Version int

	Session  SessionSubstate  `json:"session"`
	Activity ActivitySubstate `json:"activity"`

	// TerminationReason is set only when Session.State is terminated; '' otherwise.
	TerminationReason TerminationReason `json:"terminationReason,omitempty"`

	// IsAlive is the single liveness fact: is the runtime/process backing this
	// session still up? It replaces the old runtime (state, reason) axis — the
	// nuance the probe decider needs (failed-probe != dead, anti-flap) lives in
	// the decide core's inputs, not in a persisted enum.
	IsAlive bool `json:"isAlive"`

	// Harness is the agent harness the session runs (claude-code, codex, ...).
	Harness AgentHarness `json:"harness,omitempty"`

	// Detecting is the anti-flap quarantine memory. It is non-nil only while
	// the session is in the detecting state; it carries the attempt counter,
	// the first-entry time, and a hash of the (timestamp-stripped) evidence so
	// the decider can tell "same ambiguous signal N times" from "signal moved".
	Detecting *DetectingState `json:"detecting,omitempty"`
}

// ---- agent harness ----

// AgentHarness identifies which agent CLI/runtime a session drives.
type AgentHarness string

const (
	HarnessClaudeCode AgentHarness = "claude-code"
	HarnessCodex      AgentHarness = "codex"
	HarnessAider      AgentHarness = "aider"
	HarnessOpenCode   AgentHarness = "opencode"
)

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

// TerminationReason is the typed "why" for a terminated session — the only
// state that carries a reason. Empty for every non-terminal state. It decides
// the terminal display status (killed / cleanup / errored). The PR-pipeline
// "why" (fixing CI, awaiting review, …) is NOT here; it is derived on read from
// the pr table, not persisted on the session.
type TerminationReason string

const (
	TermNone               TerminationReason = ""
	TermManuallyKilled     TerminationReason = "manually_killed"
	TermRuntimeLost        TerminationReason = "runtime_lost"
	TermAgentProcessExited TerminationReason = "agent_process_exited"
	TermProbeFailure       TerminationReason = "probe_failure"
	TermErrorInProcess     TerminationReason = "error_in_process"
	TermAutoCleanup        TerminationReason = "auto_cleanup"
	TermPRMerged           TerminationReason = "pr_merged"
)

type SessionSubstate struct {
	State SessionState `json:"state"`
}

// ---- PR facts (NOT persisted on the session; sourced from the pr table) ----

// PRFacts is the per-session PR snapshot the status/reaction derivation reads
// from the pr table. It is the decider input that replaces the old persisted PR
// axis. The zero value (Exists=false) means "no PR", which derivation treats as
// "session has no PR".
type PRFacts struct {
	URL            string
	Number         int
	Exists         bool
	Draft          bool
	Merged         bool
	Closed         bool
	CI             CIState
	Review         ReviewDecision
	Mergeability   Mergeability
	ReviewComments bool // has unresolved review comments (any author) to address
}

type CIState string

const (
	CIUnknown CIState = "unknown"
	CIPending CIState = "pending"
	CIPassing CIState = "passing"
	CIFailing CIState = "failing"
)

type ReviewDecision string

const (
	ReviewNone           ReviewDecision = "none"
	ReviewApproved       ReviewDecision = "approved"
	ReviewChangesRequest ReviewDecision = "changes_requested"
	ReviewRequired       ReviewDecision = "review_required"
)

type Mergeability string

const (
	MergeUnknown     Mergeability = "unknown"
	MergeMergeable   Mergeability = "mergeable"
	MergeConflicting Mergeability = "conflicting"
	MergeBlocked     Mergeability = "blocked"
	MergeUnstable    Mergeability = "unstable"
)

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
