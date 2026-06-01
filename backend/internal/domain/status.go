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
	StatusStuck            SessionStatus = "stuck"
	StatusIdle             SessionStatus = "idle"
	StatusTerminated       SessionStatus = "terminated"
)

// DeriveStatus is the ONLY producer of display status. It is a pure function of
// persisted session facts and PR facts: is_terminated, activity_state, and the PR
// table are the durable facts that tell the UI what it needs to know.
func DeriveStatus(rec SessionRecord, pr *PRFacts) SessionStatus {
	if rec.IsTerminated {
		if pr != nil && pr.Merged {
			return StatusMerged
		}
		return StatusTerminated
	}

	switch rec.Activity.State {
	case ActivityWaitingInput:
		return StatusNeedsInput
	case ActivityBlocked:
		return StatusStuck
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
