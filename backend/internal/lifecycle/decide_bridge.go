package lifecycle

import (
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain/decide"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// defaultRecentActivityWindow is how fresh the last activity must be for the
// probe decider to treat the agent as "recently active" — which keeps an
// ambiguous dead-runtime probe in detecting instead of concluding death.
const defaultRecentActivityWindow = 60 * time.Second

// probeInput maps a raw RuntimeFacts (plus the prior detecting memory and last
// activity) into the pure decider's input. A failed/unknown probe is reported as
// such, never as a death — that routes to the detecting quarantine.
func probeInput(f ports.RuntimeFacts, cur domain.CanonicalSessionLifecycle, window time.Duration) decide.ProbeInput {
	now := nowOr(f.ObservedAt)

	var runtimeAlive, runtimeFailed bool
	switch f.Runtime {
	case ports.ProbeAlive:
		runtimeAlive = true
	case ports.ProbeFailed, ports.ProbeUnknown:
		runtimeFailed = true // ambiguous: quarantine, never conclude death
	}

	var process decide.ProcessLiveness
	var processFailed bool
	switch f.Process {
	case ports.ProbeAlive:
		process = decide.ProcessAlive
	case ports.ProbeDead:
		process = decide.ProcessDead
	case ports.ProbeFailed:
		process, processFailed = decide.ProcessIndeterminate, true
	default:
		process = decide.ProcessIndeterminate
	}

	return decide.ProbeInput{
		RuntimeAlive:   runtimeAlive,
		RuntimeFailed:  runtimeFailed,
		Process:        process,
		ProcessFailed:  processFailed,
		RecentActivity: hasRecentActivity(cur.Activity, now, window),
		Prior:          cur.Detecting,
		Now:            now,
	}
}

// hasRecentActivity answers the decider's "heard from the agent recently?"
// question. Sticky states (waiting_input/blocked) count as recent (a live-but-
// paused agent); an explicit exited never counts; else age the timestamp.
func hasRecentActivity(a domain.ActivitySubstate, now time.Time, window time.Duration) bool {
	switch {
	case a.State == domain.ActivityExited:
		return false
	case a.State.IsSticky():
		return true
	case a.LastActivityAt.IsZero():
		return false
	default:
		return now.Sub(a.LastActivityAt) <= window
	}
}

// activityToSession maps an activity classification onto the session state.
// exited returns ok=false: only the probe pipeline may conclude death.
func activityToSession(a domain.ActivityState) (domain.SessionState, bool) {
	switch a {
	case domain.ActivityActive:
		return domain.SessionWorking, true
	case domain.ActivityReady, domain.ActivityIdle:
		return domain.SessionIdle, true
	case domain.ActivityWaitingInput:
		return domain.SessionNeedsInput, true
	case domain.ActivityBlocked:
		return domain.SessionStuck, true
	default:
		return "", false
	}
}

// isTerminal reports a final session state — reopened only by an explicit
// Restore, never by an observation.
func isTerminal(s domain.SessionState) bool {
	return s == domain.SessionDone || s == domain.SessionTerminated
}

// writeRuntimeSession reports whether a probe verdict may write the session
// state. A death-axis verdict (detecting/stuck/terminated) always writes; a
// healthy "working" verdict only recovers a detecting session — it must not
// clobber an activity-owned idle/needs_input.
func writeRuntimeSession(d decide.LifecycleDecision, cur domain.CanonicalSessionLifecycle) bool {
	if isTerminal(cur.Session.State) {
		return false
	}
	if d.SessionState == domain.SessionWorking {
		return cur.Session.State == domain.SessionDetecting
	}
	return true
}

func nowOr(t time.Time) time.Time {
	if t.IsZero() {
		return time.Now()
	}
	return t
}
