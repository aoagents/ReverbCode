package domain

// SessionStatus is the single-word DISPLAY status the dashboard renders. It is
// derived from the canonical lifecycle (plus the session's PR facts) on read and
// never persisted.
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

// DeriveStatus is the ONLY producer of the display status. It is a pure, total
// function of the canonical record plus the session's PR facts (read from the pr
// table by the caller, since PR state is no longer persisted on the session).
//
// Order matters:
//  1. Terminal / hard session states (done, terminated, needs_input, stuck,
//     detecting, not_started) map directly — these OUTRANK PR facts.
//  2. Otherwise, if the session has a PR: a merged PR wins, else the PR pipeline
//     ladder (CI failure dominates, then draft/review/merge states).
//  3. Otherwise fall through to the SOFT session state (idle/working).
//
// So "PR facts dominate session facts" applies only to the soft states: an idle
// or working session with an open, CI-failing PR displays as ci_failed — but a
// session that is stuck or needs_input shows that regardless, since it needs a
// human either way.
func DeriveStatus(l CanonicalSessionLifecycle, pr PRFacts) SessionStatus {
	switch l.Session.State {
	case SessionDone:
		return StatusDone
	case SessionTerminated:
		return terminatedStatus(l.TerminationReason)
	case SessionNeedsInput:
		return StatusNeedsInput
	case SessionStuck:
		return StatusStuck
	case SessionDetecting:
		return StatusDetecting
	case SessionNotStarted:
		return StatusSpawning
	}

	if pr.Exists {
		if pr.Merged {
			return StatusMerged
		}
		if !pr.Closed {
			return prPipelineStatus(pr)
		}
	}

	if l.Session.State == SessionIdle {
		return StatusIdle
	}
	return StatusWorking
}

func terminatedStatus(r TerminationReason) SessionStatus {
	switch r {
	case TermManuallyKilled, TermRuntimeLost, TermAgentProcessExited:
		return StatusKilled
	case TermAutoCleanup, TermPRMerged:
		return StatusCleanup
	case TermErrorInProcess, TermProbeFailure:
		return StatusErrored
	default:
		return StatusTerminated
	}
}

// prPipelineStatus maps an open/draft PR's facts to a display status, preserving
// the old ladder: CI failure dominates everything, then draft, then the review /
// merge states.
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
