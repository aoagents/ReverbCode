// Package decide is the pure DECIDE core: total, deterministic, zero I/O. It
// collapses observed facts (plus the prior detecting/activity memory) into one
// LifecycleDecision. Every function here must remain side-effect free so the
// whole status truth-table can be tested in isolation.
package decide

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Anti-flap tuning. detecting escalates to stuck only after this many
// consecutive unchanged-evidence ticks OR once this much wallclock has elapsed
// since first entering detecting.
const (
	DetectingMaxAttempts = 3
	DetectingMaxDuration = 5 * time.Minute
)

// ResolveProbeDecision reconciles runtime/process liveness into a decision.
//
// The ordering encodes the load-bearing invariants:
//   - an explicit kill short-circuits straight to terminal (the only inferred
//     terminal this decider may reach without quarantine);
//   - a *failed* probe (timeout/error) is never read as death — it routes to
//     detecting, as does any disagreement between the two probes;
//   - only runtime-dead + process-dead + no-recent-activity reaches killed.
func ResolveProbeDecision(in ProbeInput) LifecycleDecision {
	if in.KillRequested {
		return LifecycleDecision{
			Status:        domain.StatusKilled,
			Evidence:      "manual kill requested",
			SessionState:  domain.SessionTerminated,
			SessionReason: domain.ReasonManuallyKilled,
		}
	}

	if in.RuntimeFailed || in.ProcessFailed || in.Runtime == domain.RuntimeProbeFailed {
		ev := fmt.Sprintf("probe_failed runtime=%s runtimeFailed=%t process=%s processFailed=%t",
			in.Runtime, in.RuntimeFailed, in.Process, in.ProcessFailed)
		return detecting(in, domain.ReasonProbeFailure, ev)
	}

	switch in.Runtime {
	case domain.RuntimeAlive:
		if in.Process == ProcessDead {
			// Runtime up but the agent process is gone: probes disagree.
			ev := fmt.Sprintf("disagree runtime=alive process=%s recentActivity=%t", in.Process, in.RecentActivity)
			return detecting(in, domain.ReasonAgentProcessExited, ev)
		}
		return LifecycleDecision{
			Status:        domain.StatusWorking,
			Evidence:      fmt.Sprintf("alive runtime=alive process=%s", in.Process),
			SessionState:  domain.SessionWorking,
			SessionReason: domain.ReasonTaskInProgress,
		}

	case domain.RuntimeExited, domain.RuntimeMissing:
		// Runtime is gone. Death is only concluded when the process is *also*
		// confirmed dead AND nothing has been heard from the agent recently;
		// any other shape is ambiguous and quarantines.
		if in.Process == ProcessAlive || in.RecentActivity {
			ev := fmt.Sprintf("disagree runtime=%s process=%s recentActivity=%t", in.Runtime, in.Process, in.RecentActivity)
			return detecting(in, domain.ReasonRuntimeLost, ev)
		}
		if in.Process == ProcessDead {
			return LifecycleDecision{
				Status:        domain.StatusKilled,
				Evidence:      fmt.Sprintf("dead runtime=%s process=dead recentActivity=false", in.Runtime),
				SessionState:  domain.SessionTerminated,
				SessionReason: domain.ReasonRuntimeLost,
			}
		}
		// Process indeterminate: cannot confirm death, so quarantine.
		ev := fmt.Sprintf("runtime_lost runtime=%s process=%s recentActivity=false", in.Runtime, in.Process)
		return detecting(in, domain.ReasonRuntimeLost, ev)

	default:
		// unknown (not yet probed): ambiguous, never conclude death.
		ev := fmt.Sprintf("runtime_unknown runtime=%s process=%s recentActivity=%t", in.Runtime, in.Process, in.RecentActivity)
		return detecting(in, domain.ReasonRuntimeLost, ev)
	}
}

// ResolveOpenPRDecision walks the PR pipeline ladder. CI failure dominates
// everything. Draft PRs then surface as draft and do not enter the review or
// merge states. Open PRs continue through requested changes, approval/merge
// states, pending review, stalled (idle-beyond-threshold), then plain open.
func ResolveOpenPRDecision(in OpenPRInput) LifecycleDecision {
	// evidence is a stable, timestamp-free summary "<condition> #<num> <url>"
	// for logs/traceability; it folds in the PR identity inputs (Number/URL).
	evidence := func(cond string) string {
		s := cond
		if in.Number > 0 {
			s += fmt.Sprintf(" #%d", in.Number)
		}
		if in.URL != "" {
			s += " " + in.URL
		}
		return s
	}
	prState := domain.PROpen
	if in.Draft {
		prState = domain.PRDraft
	}
	base := func(status domain.SessionStatus, cond string, prReason domain.PRReason, ss domain.SessionState, sr domain.SessionReason) LifecycleDecision {
		return LifecycleDecision{
			Status:        status,
			Evidence:      evidence(cond),
			SessionState:  ss,
			SessionReason: sr,
			PRState:       prState,
			PRReason:      prReason,
		}
	}

	switch {
	case in.CIFailing:
		return base(domain.StatusCIFailed, "ci_failing", domain.PRReasonCIFailing, domain.SessionWorking, domain.ReasonFixingCI)
	case in.Draft:
		return base(domain.StatusDraft, "draft", domain.PRReasonInProgress, domain.SessionWorking, domain.ReasonPRCreated)
	case in.ChangesRequested:
		return base(domain.StatusChangesRequested, "changes_requested", domain.PRReasonChangesRequested, domain.SessionWorking, domain.ReasonResolvingReviewComments)
	case in.BotComments:
		return base(domain.StatusChangesRequested, "bot_comments", domain.PRReasonBotComments, domain.SessionWorking, domain.ReasonResolvingReviewComments)
	case in.MergeConflicts:
		return base(domain.StatusPROpen, "merge_conflicts", domain.PRReasonMergeConflicts, domain.SessionWorking, domain.ReasonPRCreated)
	case in.Mergeable:
		// Mergeability is the authoritative merge gate, so it already folds in
		// "approved if review is required". Checking it before Approved means a
		// PR on a no-required-review repo (mergeable, not formally approved) is
		// still surfaced as ready-to-merge instead of falling through to PR_OPEN.
		return base(domain.StatusMergeable, "merge_ready", domain.PRReasonMergeReady, domain.SessionIdle, domain.ReasonAwaitingExternalReview)
	case in.Approved:
		return base(domain.StatusApproved, "approved", domain.PRReasonApproved, domain.SessionIdle, domain.ReasonAwaitingExternalReview)
	case in.ReviewPending:
		return base(domain.StatusReviewPending, "review_pending", domain.PRReasonReviewPending, domain.SessionIdle, domain.ReasonAwaitingExternalReview)
	case in.IdleBeyond:
		// A PR open but quiet past the stuck threshold needs a human nudge.
		return base(domain.StatusStuck, "idle_beyond", domain.PRReasonInProgress, domain.SessionStuck, domain.ReasonAwaitingUserInput)
	default:
		return base(domain.StatusPROpen, "pr_open", domain.PRReasonInProgress, domain.SessionWorking, domain.ReasonPRCreated)
	}
}

// ResolveTerminalPRStateDecision handles merged/closed PRs. A merge parks the
// session idle awaiting a human's post-merge decision; a close drops to idle.
// none/open are not terminal — callers should route those to the open-PR or
// probe deciders — but the function stays total for safety.
func ResolveTerminalPRStateDecision(pr domain.PRState) LifecycleDecision {
	switch pr {
	case domain.PRMerged:
		return LifecycleDecision{
			Status:        domain.StatusMerged,
			Evidence:      "pr merged",
			SessionState:  domain.SessionIdle,
			SessionReason: domain.ReasonMergedWaitingDecision,
			PRState:       domain.PRMerged,
			PRReason:      domain.PRReasonMerged,
		}
	case domain.PRClosed:
		return LifecycleDecision{
			Status:        domain.StatusIdle,
			Evidence:      "pr closed unmerged",
			SessionState:  domain.SessionIdle,
			SessionReason: domain.ReasonAwaitingUserInput,
			PRState:       domain.PRClosed,
			PRReason:      domain.PRReasonClosedUnmerged,
		}
	default:
		return LifecycleDecision{
			Status:        domain.StatusWorking,
			Evidence:      fmt.Sprintf("non-terminal pr state=%s", pr),
			SessionState:  domain.SessionWorking,
			SessionReason: domain.ReasonTaskInProgress,
			PRState:       pr,
		}
	}
}

// CreateDetectingDecision advances or escalates the anti-flap quarantine.
//
// The attempt counter climbs only while the (timestamp-stripped) evidence hash
// is unchanged and resets the moment the evidence moves; StartedAt is preserved
// across the whole detecting episode so the duration cap is a real wall-clock
// safety net even when the evidence keeps flapping. Escalation to stuck fires
// at DetectingMaxAttempts consecutive unchanged ticks OR DetectingMaxDuration
// elapsed since first entering detecting.
func CreateDetectingDecision(in DetectingInput) LifecycleDecision {
	hash := HashEvidence(in.Evidence)

	attempts := 1
	startedAt := in.Now
	if in.Prior != nil {
		startedAt = in.Prior.StartedAt
		if in.Prior.EvidenceHash == hash {
			attempts = in.Prior.Attempts + 1
		}
	}

	escalate := attempts >= DetectingMaxAttempts || !in.Now.Before(startedAt.Add(DetectingMaxDuration))
	if escalate {
		return LifecycleDecision{
			Status:        domain.StatusStuck,
			Evidence:      in.Evidence,
			SessionState:  domain.SessionStuck,
			SessionReason: in.ProposedReason,
		}
	}

	return LifecycleDecision{
		Status:        domain.StatusDetecting,
		Evidence:      in.Evidence,
		Detecting:     &domain.DetectingState{Attempts: attempts, StartedAt: startedAt, EvidenceHash: hash},
		SessionState:  domain.SessionDetecting,
		SessionReason: in.ProposedReason,
	}
}

// HashEvidence normalises an evidence string (stripping timestamps and
// collapsing whitespace) and hashes it, so unchanged-but-restamped signals
// compare equal and the detecting counter is not reset by clock movement alone.
func HashEvidence(evidence string) string {
	s := evidence
	for _, re := range timestampPatterns {
		s = re.ReplaceAllString(s, "")
	}
	s = strings.Join(strings.Fields(s), " ")
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// timestampPatterns is the list of regexes HashEvidence applies (in order) to
// delete the time-varying parts of an evidence string before hashing, so the
// same ambiguous signal restamped with a new clock value hashes equal and the
// detecting counter keeps climbing instead of resetting every tick.
//
// Order matters: the full datetime form is removed first so its embedded
// HH:MM:SS isn't half-eaten by the bare time-of-day pattern that follows.
//
//  1. full ISO-8601 / RFC3339 datetime — date, a T or space separator,
//     HH:MM:SS, optional fractional seconds, optional Z or ±HH:MM offset.
//     e.g. "2026-05-26T12:00:00Z", "2026-05-26 12:00:00.218+05:30"
//  2. a bare time-of-day, e.g. "12:00:00" or "12:00:00.218"
//  3. a bare unix epoch — any 10-13 digit run (seconds or millis), e.g.
//     "1716724800". This is broad enough to also clobber a same-width numeric
//     ID if one ever appears in evidence; evidence is decider-authored, so keep
//     IDs out of evidence strings to preserve hash fidelity.
var timestampPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2})?`),
	regexp.MustCompile(`\d{2}:\d{2}:\d{2}(?:\.\d+)?`),
	regexp.MustCompile(`\b\d{10,13}\b`),
}

// detecting adapts a probe verdict into the shared anti-flap path. It packages
// the proposed reason + evidence (plus the prior counter from the same probe
// input) into a DetectingInput and defers to CreateDetectingDecision, so every
// probe-driven ambiguity is counted and escalated by the identical quarantine
// logic instead of each probe branch re-implementing the counter.
func detecting(in ProbeInput, reason domain.SessionReason, evidence string) LifecycleDecision {
	return CreateDetectingDecision(DetectingInput{
		Evidence:       evidence,
		ProposedState:  domain.SessionDetecting,
		ProposedReason: reason,
		Prior:          in.Prior,
		Now:            in.Now,
	})
}
