package domain

import "time"

// Review is the per-worker code-review record: one row per worker session
// (SessionID is unique). A repeat trigger reuses this row; the per-pass facts
// live on ReviewRun.
type Review struct {
	ID        string       `json:"id"`
	SessionID SessionID    `json:"sessionId"`
	ProjectID ProjectID    `json:"projectId"`
	Harness   AgentHarness `json:"harness"`
	PRURL     string       `json:"prUrl"`
	CreatedAt time.Time    `json:"createdAt"`
	UpdatedAt time.Time    `json:"updatedAt"`
}

// ReviewRun is one review pass against a worker's PR.
type ReviewRun struct {
	ID        string          `json:"id"`
	ReviewID  string          `json:"reviewId"`
	SessionID SessionID       `json:"sessionId"`
	Harness   AgentHarness    `json:"harness"`
	PRURL     string          `json:"prUrl"`
	Status    ReviewRunStatus `json:"status"`
	Verdict   ReviewVerdict   `json:"verdict"`
	Iteration int             `json:"iteration"`
	// Body is the review text the reviewer submitted. It is recorded for AO's
	// own tracking; the reviewer also posts the review to the PR itself.
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"createdAt"`
}

// ReviewRunStatus is the lifecycle state of a single review pass.
type ReviewRunStatus string

// Review run statuses.
const (
	ReviewRunRunning  ReviewRunStatus = "running"
	ReviewRunComplete ReviewRunStatus = "complete"
	ReviewRunFailed   ReviewRunStatus = "failed"
)

// ReviewVerdict is the outcome a reviewer reports. The empty verdict marks a
// run that has not produced an outcome yet.
type ReviewVerdict string

// Review verdicts.
const (
	VerdictNone             ReviewVerdict = ""
	VerdictApproved         ReviewVerdict = "approved"
	VerdictChangesRequested ReviewVerdict = "changes_requested"
)

// Valid reports whether v is a verdict a reviewer may submit (the empty verdict
// is a stored default, not a submittable one).
func (v ReviewVerdict) Valid() bool {
	return v == VerdictApproved || v == VerdictChangesRequested
}
