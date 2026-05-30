package domain

// SessionStatus is the single-word DISPLAY status the dashboard renders. It is
// derived from the canonical lifecycle on read and never persisted.
type SessionStatus string

const (
	StatusSpawning         SessionStatus = "spawning"
	StatusWorking          SessionStatus = "working"
	StatusDetecting        SessionStatus = "detecting"
	StatusPROpen           SessionStatus = "pr_open"
	StatusDraft            SessionStatus = "draft"
	StatusCIFailed         SessionStatus = "ci_failed"
	StatusReviewPending    SessionStatus = "review_pending"
	StatusChangesRequested SessionStatus = "changes_requested"
	StatusApproved         SessionStatus = "approved"
	StatusMergeable        SessionStatus = "mergeable"
	StatusMerged           SessionStatus = "merged"
	StatusCleanup          SessionStatus = "cleanup"
	StatusNeedsInput       SessionStatus = "needs_input"
	StatusStuck            SessionStatus = "stuck"
	StatusErrored          SessionStatus = "errored"
	StatusKilled           SessionStatus = "killed"
	StatusIdle             SessionStatus = "idle"
	StatusDone             SessionStatus = "done"
	StatusTerminated       SessionStatus = "terminated"
)

// DeriveLegacyStatus is the ONLY producer of the display status. It must stay a
// pure, total function of the canonical record.
//
// Order matters:
//  1. Terminal / hard session states (done, terminated, needs_input, stuck,
//     detecting, not_started) map directly — these OUTRANK PR facts.
//  2. Otherwise a merged PR wins.
//  3. Otherwise a draft PR maps to draft, except CI failure still dominates.
//  4. Otherwise an open PR maps by its reason.
//  5. Otherwise fall through to the SOFT session state (idle/working).
//
// So "PR facts dominate session facts" applies only to the soft states: an idle
// or working session with an open, CI-failing PR displays as ci_failed — but a
// session that is stuck or needs_input shows that regardless of PR state, since
// it needs a human either way.
func DeriveLegacyStatus(l CanonicalSessionLifecycle) SessionStatus {
	switch l.Session.State {
	case SessionDone:
		return StatusDone
	case SessionTerminated:
		return terminatedStatus(l.Session.Reason)
	case SessionNeedsInput:
		return StatusNeedsInput
	case SessionStuck:
		return StatusStuck
	case SessionDetecting:
		return StatusDetecting
	case SessionNotStarted:
		return StatusSpawning
	}

	if l.PR.State == PRMerged {
		return StatusMerged
	}

	if l.PR.State == PRDraft {
		return draftPRStatus(l.PR.Reason)
	}

	if l.PR.State == PROpen {
		return openPRStatus(l.PR.Reason)
	}

	if l.Session.State == SessionIdle {
		return StatusIdle
	}
	return StatusWorking
}

func terminatedStatus(r SessionReason) SessionStatus {
	switch r {
	case ReasonManuallyKilled, ReasonRuntimeLost, ReasonAgentProcessExited:
		return StatusKilled
	case ReasonAutoCleanup, ReasonPRMerged:
		return StatusCleanup
	case ReasonErrorInProcess, ReasonProbeFailure:
		return StatusErrored
	default:
		return StatusTerminated
	}
}

func draftPRStatus(r PRReason) SessionStatus {
	if r == PRReasonCIFailing {
		return StatusCIFailed
	}
	return StatusDraft
}

func openPRStatus(r PRReason) SessionStatus {
	switch r {
	case PRReasonCIFailing:
		return StatusCIFailed
	case PRReasonChangesRequested, PRReasonBotComments:
		return StatusChangesRequested
	case PRReasonApproved:
		return StatusApproved
	case PRReasonMergeReady:
		return StatusMergeable
	case PRReasonReviewPending:
		return StatusReviewPending
	default:
		return StatusPROpen
	}
}
