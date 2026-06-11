package session

import (
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// noSignalGrace is how long after spawn/restore a session may stay silent
// before its idle reading is downgraded to StatusNoSignal. It covers the
// agent's TUI boot plus the gap to the first activity-bearing hook callback
// (for Codex that is UserPromptSubmit, seconds after the auto-submitted spawn
// prompt — its SessionStart hook fires earlier but carries no activity state);
// past it, a silent session is indistinguishable from one with a broken hook
// pipeline, and the dashboard must not claim a confident "idle".
const noSignalGrace = 90 * time.Second

// deriveStatus computes the display status. signalCapable says whether this
// session's harness has an activity hook pipeline at all; only then can
// prolonged silence mean the pipeline is broken (no_signal) rather than the
// permanent, normal silence of a hook-less harness.
func deriveStatus(rec domain.SessionRecord, pr *domain.PRFacts, now time.Time, signalCapable bool) domain.SessionStatus {
	if rec.IsTerminated {
		if pr != nil && pr.Merged {
			return domain.StatusMerged
		}
		return domain.StatusTerminated
	}

	if rec.Activity.State == domain.ActivityWaitingInput {
		return domain.StatusNeedsInput
	}

	if pr != nil {
		if pr.Merged {
			return domain.StatusMerged
		}
		if !pr.Closed {
			return prPipelineStatus(*pr)
		}
	}

	if rec.Activity.State == domain.ActivityActive {
		return domain.StatusWorking
	}

	// No hook callback has ever arrived for this spawn/restore even though the
	// harness has a hook pipeline. The seeded LastActivityAt marks the launch,
	// so once the grace passes the honest status is "no signal", not "idle".
	if signalCapable && rec.FirstSignalAt.IsZero() && now.Sub(rec.Activity.LastActivityAt) > noSignalGrace {
		return domain.StatusNoSignal
	}
	return domain.StatusIdle
}

func prPipelineStatus(pr domain.PRFacts) domain.SessionStatus {
	switch {
	case pr.CI == domain.CIFailing:
		return domain.StatusCIFailed
	case pr.Draft:
		return domain.StatusDraft
	case pr.Review == domain.ReviewChangesRequest || pr.ReviewComments:
		return domain.StatusChangesRequested
	case pr.Mergeability == domain.MergeMergeable:
		return domain.StatusMergeable
	case pr.Review == domain.ReviewApproved:
		return domain.StatusApproved
	case pr.Review == domain.ReviewRequired:
		return domain.StatusReviewPending
	default:
		return domain.StatusPROpen
	}
}
