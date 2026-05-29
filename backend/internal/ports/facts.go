// Package ports declares the boundary contracts for the lifecycle lane: the
// inbound interfaces the engine implements, the outbound interfaces its adapters
// implement, and the plain DTOs that cross those edges. It holds no logic.
package ports

import (
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// SCMFacts is produced by the SCM observer and handed to ApplySCMObservation.
//
// Fetched is the failed-fetch guard: when false, the provider query errored or
// was rate-limited and the rest of the struct must NOT be interpreted as "no PR"
// or "PR closed".
//
// CIFailureLogTail is a pointer because it is only populated when CI is failing;
// it stays off the hot poll path unless a reaction needs failure context.
type SCMFacts struct {
	Fetched          bool
	ObservedAt       time.Time
	PRState          domain.PRState
	Draft            bool
	PRNumber         int
	PRURL            string
	CISummary        CISummary
	ReviewDecision   ReviewDecision
	Mergeability     Mergeability
	PendingComments  []ReviewComment
	CIFailedChecks   []CICheck
	CIFailureLogTail *string
}

type CISummary string

const (
	CIPending CISummary = "pending"
	CIPassing CISummary = "passing"
	CIFailing CISummary = "failing"
	CINone    CISummary = "none"
)

type ReviewDecision string

const (
	ReviewApproved         ReviewDecision = "approved"
	ReviewChangesRequested ReviewDecision = "changes_requested"
	ReviewPending          ReviewDecision = "pending"
	ReviewNone             ReviewDecision = "none"
)

// Mergeability carries provider merge gates. The LCM projects it into the
// existing domain.Mergeability display state; the richer fields stay available
// to reactions.
type Mergeability struct {
	Mergeable   bool
	CIPassing   bool
	Approved    bool
	NoConflicts bool
	Conflict    bool
	BehindBase  bool
	Unknown     bool
	Blockers    []string
}

type CICheck struct {
	Name       string
	Status     string
	Conclusion string
	URL        string
	Details    string
	LogTail    string
}

// ReviewComment carries provider review-thread details. IsBot lets the SCM
// provider distinguish bot comments from human review feedback without putting
// provider-specific bot rules in the common observer.
type ReviewComment struct {
	Author   string
	Body     string
	IsBot    bool
	URL      string
	Path     string
	Line     int
	ThreadID string
}

// ProbeResult is a single liveness reading. "failed" (the probe errored/timed
// out) and "unknown" (ran but couldn't tell) are kept distinct from dead — both
// route to the detecting quarantine, never to a death conclusion.
type ProbeResult string

const (
	ProbeAlive   ProbeResult = "alive"
	ProbeDead    ProbeResult = "dead"
	ProbeFailed  ProbeResult = "failed"
	ProbeUnknown ProbeResult = "unknown"
)

// RuntimeFacts is what the reaper reports each probe: is the runtime container
// up, and is the agent process inside it up.
type RuntimeFacts struct {
	ObservedAt time.Time
	Runtime    ProbeResult
	Process    ProbeResult
}

// ActivitySignal is pushed by the agent hooks. Only a Valid signal is
// authoritative; a stale/absent one is ignored rather than read as idleness.
type ActivitySignal struct {
	Valid     bool
	State     domain.ActivityState
	Timestamp time.Time
	Source    domain.ActivitySource
}

// PRObservation is what the SCM poller reports for one PR. Fetched is the
// failed-fetch guard: when false the rest is meaningless and the engine must not
// read it as "PR closed". Checks/Comments are the current full sets (the engine
// records the checks and replaces the comment set).
type PRObservation struct {
	Fetched      bool
	URL          string
	Number       int
	Draft        bool
	Merged       bool
	Closed       bool
	CI           domain.CIState
	Review       domain.ReviewDecision
	Mergeability domain.Mergeability
	Checks       []PRCheckRow
	Comments     []PRComment
}

// SpawnOutcome is what the Session Manager reports once a spawn is live: the
// handles needed for later teardown/restore.
type SpawnOutcome struct {
	Branch         string
	WorkspacePath  string
	RuntimeHandle  RuntimeHandle
	AgentSessionID string
	Prompt         string
}

// ---- store row DTOs (shared by the PRWriter port and its sqlite adapter) ----

// PRRow is the scalar PR facts row.
type PRRow struct {
	URL          string
	SessionID    string
	Number       int
	Draft        bool
	Merged       bool
	Closed       bool
	CI           domain.CIState
	Review       domain.ReviewDecision
	Mergeability domain.Mergeability
	UpdatedAt    time.Time
}

// PRCheckRow is one CI check run (one row per check name per commit).
type PRCheckRow struct {
	PRURL      string
	Name       string
	CommitHash string
	Status     string
	URL        string
	LogTail    string
	CreatedAt  time.Time
}

// PRComment is one review comment. Review feedback is injected into the agent
// regardless of author, so there is no bot/human distinction.
type PRComment struct {
	ID        string
	Author    string
	File      string
	Line      int
	Body      string
	Resolved  bool
	CreatedAt time.Time
}
