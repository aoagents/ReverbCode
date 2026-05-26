package lifecycle

import (
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain/decide"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// defaultRecentActivityWindow is how fresh the last activity signal must be for
// the probe decider to treat the agent as "recently active" (which keeps an
// ambiguous dead-runtime probe in detecting instead of concluding death).
const defaultRecentActivityWindow = 60 * time.Second

// ---- fact translation: ports DTOs -> pure decide inputs ----

// runtimeFactsToProbeInput maps a raw RuntimeFacts (plus the prior detecting
// memory and last-known activity read back from canonical) into the probe
// decider's input. KillRequested is always false here: the inferred-death path
// never carries an explicit kill — that arrives via OnKillRequested.
func runtimeFactsToProbeInput(f ports.RuntimeFacts, cur domain.CanonicalSessionLifecycle, window time.Duration) decide.ProbeInput {
	rt, rtFailed := runtimeProbeToState(f.RuntimeState)
	proc, procFailed := processProbeToLiveness(f.ProcessState)
	now := nowOr(f.ObservedAt)
	return decide.ProbeInput{
		Runtime:        rt,
		RuntimeFailed:  rtFailed,
		Process:        proc,
		ProcessFailed:  procFailed,
		RecentActivity: hasRecentActivity(cur.Activity, now, window),
		Prior:          cur.Detecting,
		Now:            now,
	}
}

func runtimeProbeToState(p ports.RuntimeProbe) (domain.RuntimeState, bool) {
	switch p {
	case ports.RuntimeProbeAlive:
		return domain.RuntimeAlive, false
	case ports.RuntimeProbeDead:
		return domain.RuntimeExited, false
	case ports.RuntimeProbeFailed:
		return domain.RuntimeProbeFailed, true
	default: // indeterminate / unset: ambiguous, never a death conclusion
		return domain.RuntimeUnknown, false
	}
}

func processProbeToLiveness(p ports.ProcessProbe) (decide.ProcessLiveness, bool) {
	switch p {
	case ports.ProcessProbeAlive:
		return decide.ProcessAlive, false
	case ports.ProcessProbeDead:
		return decide.ProcessDead, false
	case ports.ProcessProbeFailed:
		return decide.ProcessIndeterminate, true
	default: // indeterminate / unset
		return decide.ProcessIndeterminate, false
	}
}

// runtimeSubstateFromFacts derives the runtime sub-state to persist. Liveness
// always owns this axis, so it is written on every runtime observation
// regardless of what the session axis does.
func runtimeSubstateFromFacts(f ports.RuntimeFacts) domain.RuntimeSubstate {
	switch f.RuntimeState {
	case ports.RuntimeProbeAlive:
		return domain.RuntimeSubstate{State: domain.RuntimeAlive, Reason: domain.RuntimeReasonProcessRunning}
	case ports.RuntimeProbeDead:
		return domain.RuntimeSubstate{State: domain.RuntimeExited, Reason: domain.RuntimeReasonTmuxMissing}
	case ports.RuntimeProbeFailed:
		return domain.RuntimeSubstate{State: domain.RuntimeProbeFailed, Reason: domain.RuntimeReasonProbeError}
	case ports.RuntimeProbeIndeterminate:
		// Probe ran but couldn't tell — distinct from a probe error, so no
		// probe_error reason; the ambiguity is carried by RuntimeUnknown alone.
		return domain.RuntimeSubstate{State: domain.RuntimeUnknown}
	default: // unset
		return domain.RuntimeSubstate{State: domain.RuntimeUnknown}
	}
}

// hasRecentActivity answers the probe decider's "was the agent heard from
// recently?" question. Sticky states (waiting_input/blocked) count as recent
// because they mean a live-but-paused agent; an explicit exited signal never
// counts; otherwise we age the last-activity timestamp against the window.
func hasRecentActivity(a domain.ActivitySubstate, now time.Time, window time.Duration) bool {
	if a.State == domain.ActivityExited {
		return false
	}
	if a.State.IsSticky() {
		return true
	}
	if a.LastActivityAt.IsZero() {
		return false
	}
	return now.Sub(a.LastActivityAt) <= window
}

// openPRInput maps SCM facts onto the open-PR ladder. IdleBeyond is always false
// in split A — the idle-duration signal is owned by the escalation engine
// (split B); the synchronous LCM has no clock of its own here.
func openPRInput(f ports.SCMFacts) decide.OpenPRInput {
	return decide.OpenPRInput{
		CIFailing:        f.CISummary == ports.CIFailing,
		ChangesRequested: f.ReviewDecision == ports.ReviewChangesRequested,
		Approved:         f.ReviewDecision == ports.ReviewApproved,
		Mergeable:        f.Mergeability.Mergeable,
		ReviewPending:    f.ReviewDecision == ports.ReviewPending,
		Number:           f.PRNumber,
		URL:              f.PRURL,
	}
}

// ---- activity -> session axis mapping (activity owns working/idle/waiting) ----

// activityToSession maps an activity classification onto the session sub-state.
// exited returns ok=false: an exit signal must NOT write a terminal session
// state — only the probe pipeline (via detecting) may conclude inferred death.
func activityToSession(a domain.ActivityState) (domain.SessionState, domain.SessionReason, bool) {
	switch a {
	case domain.ActivityActive:
		return domain.SessionWorking, domain.ReasonTaskInProgress, true
	case domain.ActivityReady:
		// ready = the agent finished a unit and is waiting for more work.
		return domain.SessionIdle, domain.ReasonResearchComplete, true
	case domain.ActivityIdle:
		// plain inactivity carries no completion claim, so no specific reason
		// (research_complete here would read misleadingly in diagnostics).
		return domain.SessionIdle, "", true
	case domain.ActivityWaitingInput:
		return domain.SessionNeedsInput, domain.ReasonAwaitingUserInput, true
	case domain.ActivityBlocked:
		return domain.SessionStuck, domain.ReasonAwaitingUserInput, true
	default: // exited / unset
		return "", "", false
	}
}

// ---- composition predicates: who may write the session axis ----

// isTerminal reports a final session state that must not be resurrected by an
// observation (only an explicit Restore reopens a terminal session).
func isTerminal(s domain.SessionState) bool {
	return s == domain.SessionDone || s == domain.SessionTerminated
}

// isLivenessOwned reports whether the current session sub-state was set by the
// liveness/death axis (the probe pipeline) and may therefore be recovered by a
// later healthy probe. detecting is always liveness-owned; a stuck/terminated
// state is liveness-owned only when its reason came from a death inference.
func isLivenessOwned(s domain.SessionSubstate) bool {
	if s.State == domain.SessionDetecting {
		return true
	}
	switch s.Reason {
	case domain.ReasonRuntimeLost, domain.ReasonAgentProcessExited, domain.ReasonProbeFailure:
		return true
	}
	return false
}

// shouldWriteSessionRuntime is the #1 composition rule for ApplyRuntimeObservation.
// A death-axis verdict (detecting/stuck/terminal) always writes — it overrides
// activity because a (maybe) dead agent can't be working/waiting. A healthy
// "working" verdict only writes when it is recovering a liveness-owned state
// (e.g. detecting -> working); it must NOT clobber an activity-owned
// needs_input/blocked/idle the activity axis is responsible for.
func shouldWriteSessionRuntime(d decide.LifecycleDecision, cur domain.CanonicalSessionLifecycle) bool {
	if isTerminal(cur.Session.State) {
		// A terminal session is only reopened by an explicit Restore — never by
		// an observation. Even a death-axis verdict (e.g. detecting) must not
		// resurrect it; the runtime axis is still patched separately.
		return false
	}
	if d.SessionState == domain.SessionWorking {
		return isLivenessOwned(cur.Session)
	}
	return true
}

// shouldWriteSessionActivity is the mirror rule for ApplyActivitySignal: the
// activity axis owns working/idle/waiting. A valid activity signal is direct
// proof of life, so it is allowed to RESOLVE a detecting session (pull it out of
// the liveness quarantine) — but it must not resurrect a terminal session, and
// it leaves a liveness-escalated stuck state to the probe pipeline (stuck is a
// deliberate human-facing escalation, not a transient quarantine).
func shouldWriteSessionActivity(cur domain.CanonicalSessionLifecycle) bool {
	if isTerminal(cur.Session.State) {
		return false
	}
	if cur.Session.State == domain.SessionDetecting {
		return true
	}
	return !isLivenessOwned(cur.Session)
}

// ---- explicit-kill mapping (SM's terminal-write authority) ----

func killSession(k ports.LifecycleKillReason) domain.SessionSubstate {
	switch k {
	case ports.KillManual:
		return domain.SessionSubstate{State: domain.SessionTerminated, Reason: domain.ReasonManuallyKilled}
	case ports.KillCleanup:
		return domain.SessionSubstate{State: domain.SessionTerminated, Reason: domain.ReasonAutoCleanup}
	default: // error
		return domain.SessionSubstate{State: domain.SessionTerminated, Reason: domain.ReasonErrorInProcess}
	}
}

func killRuntime(k ports.LifecycleKillReason) domain.RuntimeSubstate {
	switch k {
	case ports.KillManual:
		return domain.RuntimeSubstate{State: domain.RuntimeExited, Reason: domain.RuntimeReasonManualKillRequested}
	case ports.KillCleanup:
		return domain.RuntimeSubstate{State: domain.RuntimeExited, Reason: domain.RuntimeReasonAutoCleanup}
	default: // error
		return domain.RuntimeSubstate{State: domain.RuntimeExited, Reason: domain.RuntimeReasonProbeError}
	}
}

func nowOr(t time.Time) time.Time {
	if t.IsZero() {
		return time.Now()
	}
	return t
}
