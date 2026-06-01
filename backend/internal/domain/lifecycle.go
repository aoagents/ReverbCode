// Package domain holds shared vocabulary for sessions, activity, and PR facts.
// Session state is deliberately small: durable session rows carry activity_state
// plus an is_terminated bit; user-facing status is derived from those fields and
// PR facts at read time.
package domain

import "time"

// ---- agent harness ----

// AgentHarness identifies which agent CLI/runtime a session drives.
type AgentHarness string

// Supported agent harnesses.
const (
	HarnessClaudeCode AgentHarness = "claude-code"
	HarnessCodex      AgentHarness = "codex"
	HarnessAider      AgentHarness = "aider"
	HarnessOpenCode   AgentHarness = "opencode"
)

// ---- activity state (the only persisted status-like session fact) ----

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

// ActivitySubstate is the persisted activity reading: the state, when it was
// last observed, and which source reported it.
type ActivitySubstate struct {
	State          ActivityState  `json:"state"`
	LastActivityAt time.Time      `json:"lastActivityAt"`
	Source         ActivitySource `json:"source"`
}
