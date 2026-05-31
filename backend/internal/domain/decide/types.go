package decide

import (
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// LifecycleDecision is the output of a decider: the canonical session sub-state
// to persist (state, the liveness bool, and — only for a terminal state — the
// termination reason), the human-readable evidence, and the (possibly updated)
// detecting memory. The display status is NOT here — it is derived on read by
// domain.DeriveStatus from the persisted lifecycle plus the pr table.
//
// PR facts are likewise not here: a liveness verdict knows nothing about the PR,
// and PR-driven display/reactions are handled off the pr table, not the session
// state machine.
type LifecycleDecision struct {
	Evidence          string
	Detecting         *domain.DetectingState
	SessionState      domain.SessionState
	TerminationReason domain.TerminationReason // set only when SessionState is terminated
	IsAlive           bool
}

// ProbeInput reconciles runtime + process liveness. A *failed* probe (timeout or
// error) is distinct from a "dead" verdict and must route to detecting, never to
// a death conclusion. KillRequested short-circuits to terminal with KillReason.
type ProbeInput struct {
	RuntimeAlive   bool // the runtime probe reports the backing runtime is up
	RuntimeFailed  bool // the runtime probe itself failed (timeout/error) — not "dead"
	Process        ProcessLiveness
	ProcessFailed  bool
	RecentActivity bool
	KillRequested  bool
	KillReason     domain.TerminationReason // the terminal reason when KillRequested
	Prior          *domain.DetectingState
	Now            time.Time
}

// ProcessLiveness mirrors isProcessRunning's three-valued answer.
type ProcessLiveness string

const (
	ProcessAlive         ProcessLiveness = "alive"
	ProcessDead          ProcessLiveness = "dead"
	ProcessIndeterminate ProcessLiveness = "indeterminate"
)

// DetectingInput feeds the anti-flap quarantine counter. Evidence is hashed with
// timestamps stripped, so "same ambiguous signal" keeps the counter climbing
// while any real change resets it.
type DetectingInput struct {
	Evidence string
	Prior    *domain.DetectingState
	Now      time.Time
}
