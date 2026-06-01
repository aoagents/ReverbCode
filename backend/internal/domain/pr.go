package domain

import "time"

// ---- PR read model ----

// PRFacts is the per-session PR snapshot the status derivation reads from the
// pr table. The zero value (Exists=false) means "no PR".
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

// CIState is the aggregate CI status of a PR.
type CIState string

// CI states.
const (
	CIUnknown CIState = "unknown"
	CIPending CIState = "pending"
	CIPassing CIState = "passing"
	CIFailing CIState = "failing"
)

// ReviewDecision is the aggregate human-review verdict on a PR.
type ReviewDecision string

// Review decisions.
const (
	ReviewNone           ReviewDecision = "none"
	ReviewApproved       ReviewDecision = "approved"
	ReviewChangesRequest ReviewDecision = "changes_requested"
	ReviewRequired       ReviewDecision = "review_required"
)

// Mergeability is whether a PR can currently be merged.
type Mergeability string

// Mergeability states.
const (
	MergeUnknown     Mergeability = "unknown"
	MergeMergeable   Mergeability = "mergeable"
	MergeConflicting Mergeability = "conflicting"
	MergeBlocked     Mergeability = "blocked"
	MergeUnstable    Mergeability = "unstable"
)

// The PR rows are the canonical shapes for the pr / pr_checks / pr_comment
// tables, shared by the PRWriter port and the sqlite store (the store maps them
// to/from the sqlc gen.* models). They are flat by design — these tables carry
// no nesting or derivation, so a single definition serves every layer.

// PRRow is the scalar facts of one tracked pull request (the pr table). A session
// can own several PRs; a PR belongs to one session. PRFacts is the read-model derived from these for display status; PRRow is what gets written.
type PRRow struct {
	URL          string
	SessionID    SessionID
	Number       int
	Draft        bool
	Merged       bool
	Closed       bool
	CI           CIState
	Review       ReviewDecision
	Mergeability Mergeability
	UpdatedAt    time.Time
}

// PRState is the normalized lifecycle of one tracked pull request as stored in
// the pr table.
type PRState string

// PR states.
const (
	PRStateDraft  PRState = "draft"
	PRStateOpen   PRState = "open"
	PRStateMerged PRState = "merged"
	PRStateClosed PRState = "closed"
)

// PRCheckStatus is one CI check run's normalized status.
type PRCheckStatus string

// PR check statuses.
const (
	PRCheckUnknown    PRCheckStatus = "unknown"
	PRCheckQueued     PRCheckStatus = "queued"
	PRCheckInProgress PRCheckStatus = "in_progress"
	PRCheckPassed     PRCheckStatus = "passed"
	PRCheckFailed     PRCheckStatus = "failed"
	PRCheckSkipped    PRCheckStatus = "skipped"
	PRCheckCancelled  PRCheckStatus = "cancelled"
)

// PRCheckRow is one CI check run — one row per check name per commit.
type PRCheckRow struct {
	PRURL      string
	Name       string
	CommitHash string
	Status     PRCheckStatus
	URL        string
	LogTail    string
	CreatedAt  time.Time
}

// PRComment is one review comment. Feedback is injected into the agent
// regardless of author, so there is no bot/human distinction.
type PRComment struct {
	ID        string
	Author    string
	File      string
	Line      int
	Body      string
	Resolved  bool
	CreatedAt time.Time
}
