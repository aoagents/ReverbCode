package domain

// SessionStatus is the single-word DISPLAY status the dashboard renders. It is
// derived from persisted session facts plus PR facts and is never stored.
//
// There are five states, one per distinct move a human makes when scanning a
// wall of agents: leave it alone, respond, act on a clean PR, get it moving, or
// nothing. Finer PR detail (CI failing vs changes requested vs approved) lives
// in the inspector, not in the glanceable status.
type SessionStatus string

// The display statuses the dashboard renders.
const (
	// StatusWorking — the agent is actively running. Leave it alone.
	StatusWorking SessionStatus = "working"
	// StatusNeedsInput — the agent is blocked on you. Respond.
	StatusNeedsInput SessionStatus = "needs_input"
	// StatusReady — a clean PR is waiting on you (mergeable, approved, or needs
	// your review). Merge it / go review it.
	StatusReady SessionStatus = "ready"
	// StatusStalled — the agent will not finish on its own (hung, never booted,
	// or stopped with unfinished work). Get it moving.
	StatusStalled SessionStatus = "stalled"
	// StatusIdle — nothing is happening, or the work is finished (also covers
	// merged and terminated). Nothing to do.
	StatusIdle SessionStatus = "idle"
)
