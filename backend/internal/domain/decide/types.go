package decide

import (
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// LifecycleDecision is the output of every decider: the derived display status
// plus the canonical sub-state values to persist, the human-readable evidence,
// and the (possibly updated) detecting memory.
//
// Zero-value sub-state fields mean "this decider does not address that
// sub-state — leave it unchanged", NOT "set it to the empty value". SessionState
// is always populated, but the probe/detecting/kill paths legitimately leave
// PRState/PRReason empty: a liveness verdict knows nothing about the PR. When
// the LCM folds a decision into the next full canonical row it must therefore
// leave empty PRState/PRReason unchanged rather than writing them through —
// writing PRNone on a routine probe tick would clobber a live PR. Detecting is
// nil-by-default; the LCM explicitly clears stale detecting memory when a probe
// verdict leaves detecting.
type LifecycleDecision struct {
	Status        domain.SessionStatus
	Evidence      string
	Detecting     *domain.DetectingState
	SessionState  domain.SessionState
	SessionReason domain.SessionReason
	PRState       domain.PRState
	PRReason      domain.PRReason
}

// ProbeInput reconciles runtime + process liveness. A *failed* probe (timeout
// or error) is distinct from a "dead" verdict and must route to detecting,
// never to a death conclusion. KillRequested short-circuits to terminal.
type ProbeInput struct {
	Runtime        domain.RuntimeState
	RuntimeFailed  bool
	Process        ProcessLiveness
	ProcessFailed  bool
	RecentActivity bool
	KillRequested  bool
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

// OpenPRInput drives the PR pipeline ladder for an open or draft PR.
type OpenPRInput struct {
	Draft            bool
	CIFailing        bool
	ChangesRequested bool
	BotComments      bool
	MergeConflicts   bool
	Approved         bool
	Mergeable        bool
	ReviewPending    bool
	IdleBeyond       bool // idle past the stuck threshold
	Number           int
	URL              string
}

// DetectingInput feeds the quarantine counter. Evidence is hashed with
// timestamps stripped, so "same ambiguous signal" keeps the counter climbing
// while any real change resets it.
type DetectingInput struct {
	Evidence       string
	ProposedState  domain.SessionState
	ProposedReason domain.SessionReason
	Prior          *domain.DetectingState
	Now            time.Time
}
