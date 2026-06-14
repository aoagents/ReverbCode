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
//
// A session may own several PRs at once (independent or stacked). The PR-derived
// status is the worst-wins aggregate across its open PRs; stacked children whose
// parent is still open are exempt from the aggregation since they cannot merge
// until the parent does. Merged/closed PRs only matter once no open PR remains.
func deriveStatus(rec domain.SessionRecord, prs []domain.PRFacts, now time.Time, signalCapable bool) domain.SessionStatus {
	if rec.IsTerminated {
		if anyMerged(prs) {
			return domain.StatusMerged
		}
		return domain.StatusTerminated
	}

	if rec.Activity.State == domain.ActivityWaitingInput {
		return domain.StatusNeedsInput
	}

	open := openPRs(prs)
	if len(open) > 0 {
		return aggregatePRStatus(open)
	}
	if anyMerged(prs) {
		return domain.StatusMerged
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

// openPRs returns the PRs that are neither merged nor closed, preserving order.
func openPRs(prs []domain.PRFacts) []domain.PRFacts {
	out := make([]domain.PRFacts, 0, len(prs))
	for _, p := range prs {
		if !p.Merged && !p.Closed {
			out = append(out, p)
		}
	}
	return out
}

func anyMerged(prs []domain.PRFacts) bool {
	for _, p := range prs {
		if p.Merged {
			return true
		}
	}
	return false
}

// aggregatePRStatus is the worst-wins reduction over a session's open PRs.
// Stacked children blocked by an open parent are excluded: they cannot merge
// yet, so their pipeline state is not a user-actionable signal for the session.
// If every open PR is blocked (a degenerate stack with no visible root), it
// falls back to aggregating across all of them so the session never goes dark.
func aggregatePRStatus(open []domain.PRFacts) domain.SessionStatus {
	stacks := buildStacks(open)
	candidates := make([]domain.PRFacts, 0, len(open))
	for _, p := range open {
		if !stacks[p.URL].Blocked {
			candidates = append(candidates, p)
		}
	}
	if len(candidates) == 0 {
		candidates = open
	}
	worst := prPipelineStatus(candidates[0])
	for _, p := range candidates[1:] {
		if s := prPipelineStatus(p); statusSeverity(s) < statusSeverity(worst) {
			worst = s
		}
	}
	return worst
}

// statusSeverity ranks PR pipeline statuses from most to least urgent so the
// aggregate surfaces the PR that most needs attention. mergeable is least urgent
// so a session only reports mergeable when every aggregated PR is mergeable.
func statusSeverity(s domain.SessionStatus) int {
	switch s {
	case domain.StatusCIFailed:
		return 0
	case domain.StatusChangesRequested:
		return 1
	case domain.StatusDraft:
		return 2
	case domain.StatusReviewPending:
		return 3
	case domain.StatusPROpen:
		return 4
	case domain.StatusApproved:
		return 5
	case domain.StatusMergeable:
		return 6
	default:
		return 7
	}
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
