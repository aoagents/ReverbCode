package domain

import "time"

// ---- PR read model ----

// PRFacts is the per-session PR snapshot the status derivation reads from the
// pr table.
type PRFacts struct {
	URL            string
	Number         int
	Draft          bool
	Merged         bool
	Closed         bool
	CI             CIState
	Review         ReviewDecision
	Mergeability   Mergeability
	ReviewComments bool // has unresolved review comments (any author) to address
}

// PullRequest is the app-level representation of one tracked pull request as
// persisted by the PR store. It is intentionally separate from the sqlc
// generated sqlite row type so storage details do not leak outside sqlite.
type PullRequest struct {
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

// PullRequestCheck is one normalized CI check run for a pull request.
type PullRequestCheck struct {
	Name       string
	CommitHash string
	Status     PRCheckStatus
	URL        string
	LogTail    string
	CreatedAt  time.Time
}

// PullRequestComment is one normalized review comment for a pull request.
type PullRequestComment struct {
	ID        string
	Author    string
	File      string
	Line      int
	Body      string
	Resolved  bool
	CreatedAt time.Time
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
