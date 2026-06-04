package domain

// SessionStatus is the single-word DISPLAY status the dashboard renders. It is
// derived from persisted session facts plus PR facts and is never stored.
type SessionStatus string

// The display statuses the dashboard renders.
const (
	StatusWorking          SessionStatus = "working"
	StatusPROpen           SessionStatus = "pr_open"
	StatusDraft            SessionStatus = "draft"
	StatusCIFailed         SessionStatus = "ci_failed"
	StatusReviewPending    SessionStatus = "review_pending"
	StatusChangesRequested SessionStatus = "changes_requested"
	StatusApproved         SessionStatus = "approved"
	StatusMergeable        SessionStatus = "mergeable"
	StatusMerged           SessionStatus = "merged"
	StatusNeedsInput       SessionStatus = "needs_input"
	StatusIdle             SessionStatus = "idle"
	StatusTerminated       SessionStatus = "terminated"
)

// DeriveStatus computes the user-facing display status from a SessionRecord and
// optional PR facts. pr nil means the session has no associated PR.
func DeriveStatus(rec SessionRecord, pr *PRFacts) SessionStatus {
	if rec.IsTerminated {
		if pr != nil && pr.Merged {
			return StatusMerged
		}
		return StatusTerminated
	}
	if rec.Activity.State == ActivityWaitingInput {
		return StatusNeedsInput
	}
	if pr != nil {
		if pr.Merged {
			return StatusMerged
		}
		if !pr.Closed {
			return prPipelineStatus(*pr)
		}
	}
	if rec.Activity.State == ActivityActive {
		return StatusWorking
	}
	return StatusIdle
}

func prPipelineStatus(pr PRFacts) SessionStatus {
	switch {
	case pr.CI == CIFailing:
		return StatusCIFailed
	case pr.Draft:
		return StatusDraft
	case pr.Review == ReviewChangesRequest || pr.ReviewComments:
		return StatusChangesRequested
	case pr.Mergeability == MergeMergeable:
		return StatusMergeable
	case pr.Review == ReviewApproved:
		return StatusApproved
	case pr.Review == ReviewRequired:
		return StatusReviewPending
	default:
		return StatusPROpen
	}
}
