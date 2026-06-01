package domain

import "time"

// ActivityState is how busy the agent is, derived from its output/JSONL.
type ActivityState string

// Activity states. WaitingInput and Blocked are sticky (see IsSticky).
const (
	ActivityActive       ActivityState = "active"
	ActivityReady        ActivityState = "ready"
	ActivityIdle         ActivityState = "idle"
	ActivityWaitingInput ActivityState = "waiting_input"
	ActivityBlocked      ActivityState = "blocked"
	ActivityExited       ActivityState = "exited"
)

// IsSticky reports whether an activity state must NOT be aged/demoted by the
// passage of time (a paused agent is still paused until a new signal says so).
func (a ActivityState) IsSticky() bool {
	return a == ActivityWaitingInput || a == ActivityBlocked
}

// ActivitySource records where an activity reading came from, so a weaker
// source can't override a stronger one.
type ActivitySource string

// Activity signal sources, strongest first.
const (
	SourceNative   ActivitySource = "native"
	SourceTerminal ActivitySource = "terminal"
	SourceHook     ActivitySource = "hook"
	SourceRuntime  ActivitySource = "runtime"
	SourceNone     ActivitySource = "none"
)

// CanOverride reports whether a reading from source a may replace a current
// reading from source current. Unknown sources are treated as weakest.
func (a ActivitySource) CanOverride(current ActivitySource) bool {
	return activitySourceRank(a) <= activitySourceRank(current)
}

func activitySourceRank(s ActivitySource) int {
	switch s {
	case SourceNative:
		return 0
	case SourceTerminal:
		return 1
	case SourceHook:
		return 2
	case SourceRuntime:
		return 3
	default:
		return 4
	}
}

// ActivitySubstate is the persisted activity reading: the state, when it was
// last observed, and which source reported it.
type ActivitySubstate struct {
	State          ActivityState  `json:"state"`
	LastActivityAt time.Time      `json:"lastActivityAt"`
	Source         ActivitySource `json:"source"`
}
