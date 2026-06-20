package session

import (
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// bootGrace is how long after spawn/restore a signal-capable session may stay
// silent before its silence reads as a broken pipeline (never booted) rather
// than a normal boot gap. It covers the agent's TUI boot plus the gap to the
// first activity-bearing hook callback.
const bootGrace = 120 * time.Second

// hangTimeout is how long an "active" session may go silent before it reads as
// hung mid-run (wedged on a tool call or rate limit) instead of working. The
// detector trades false positives for catching the hang, since a calmly
// breathing "Working" on a wedged agent is the failure that actually costs you.
// This is an open number that wants tuning against live sessions.
const hangTimeout = 10 * time.Minute

// deriveStatus computes the display status as one of five states. It reads the
// raw activity state plus the PR buckets directly, never a pre-collapsed
// status, so the active-vs-PR precedence below resolves the right way.
//
// Evaluated top to bottom, first match wins: a hung-or-never-booted session is
// caught before it can read Working, and the whole silent case outranks "go
// review". signalCapable says whether this harness has an activity hook
// pipeline at all; only then can prolonged silence mean the pipeline is broken.
//
// A session may own several PRs at once (independent or stacked). prMoves folds
// them into two booleans: a clean PR with a real action waiting on you, and an
// unfinished PR the agent left undone. A blocked stacked child's readiness is
// suppressed (it cannot merge until its parent does), but its problems still
// count as unfinished work.
func deriveStatus(rec domain.SessionRecord, prs []domain.PRFacts, now time.Time, signalCapable bool) domain.SessionStatus {
	hasClean, hasUnfinished := prMoves(openPRs(prs))

	switch {
	case rec.IsTerminated: // includes merged
		return domain.StatusIdle
	case rec.Activity.State == domain.ActivityWaitingInput:
		return domain.StatusNeedsInput
	case stalled(rec, now, signalCapable, hasUnfinished):
		return domain.StatusStalled
	case rec.Activity.State == domain.ActivityActive:
		return domain.StatusWorking
	case hasClean:
		return domain.StatusReady
	default:
		return domain.StatusIdle
	}
}

// stalled reports whether the agent will not finish on its own: it never booted,
// hung mid-run, or stopped leaving its PR undone.
func stalled(rec domain.SessionRecord, now time.Time, signalCapable, hasUnfinished bool) bool {
	active := rec.Activity.State == domain.ActivityActive
	silence := now.Sub(rec.Activity.LastActivityAt)
	switch {
	case signalCapable && rec.FirstSignalAt.IsZero() && silence > bootGrace:
		return true // never booted
	case active && silence > hangTimeout:
		return true // hung mid-run
	case !active && hasUnfinished:
		return true // stopped, work undone
	default:
		return false
	}
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

// prMoves folds a session's open PRs into whose move it is. hasClean is true
// when some PR has a real action waiting on you; hasUnfinished is true when some
// PR is work the agent left undone. A blocked stacked child cannot merge until
// its parent does, so its clean signal is suppressed, but its unfinished signal
// still surfaces so a broken child is not hidden behind the stack.
//
// A pathological all-blocked-clean stack (a cycle of mergeable PRs each targeting
// the next's branch, with no root) yields neither move and reads Idle. The old
// model force-aggregated such a set to avoid "going dark"; the new model treats
// genuinely no-actionable-move PRs as Idle, which is honest, and the cycle cannot
// arise from a real branch topology.
func prMoves(open []domain.PRFacts) (hasClean, hasUnfinished bool) {
	stacks := buildStacks(open)
	for _, p := range open {
		switch prBucket(p) {
		case bucketUnfinished:
			hasUnfinished = true
		case bucketClean:
			if !stacks[p.URL].Blocked {
				hasClean = true
			}
		}
	}
	return hasClean, hasUnfinished
}

type prBucketKind int

const (
	bucketNeutral prBucketKind = iota // bare open PR, just sitting there
	bucketClean                       // a clean action waits on you
	bucketUnfinished                  // the agent has more to do
)

// prBucket sorts one PR by whose move it is. Unfinished signals (failing CI,
// draft, requested changes, unresolved comments, merge conflict) are checked
// first so a PR that is both broken and otherwise mergeable reads as unfinished.
func prBucket(pr domain.PRFacts) prBucketKind {
	switch {
	case pr.CI == domain.CIFailing,
		pr.Draft,
		pr.Review == domain.ReviewChangesRequest,
		pr.ReviewComments,
		pr.Mergeability == domain.MergeConflicting:
		return bucketUnfinished
	case pr.Mergeability == domain.MergeMergeable,
		pr.Review == domain.ReviewApproved,
		pr.Review == domain.ReviewRequired:
		return bucketClean
	default:
		return bucketNeutral
	}
}
