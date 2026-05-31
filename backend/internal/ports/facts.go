// Package ports declares the boundary contracts for the lifecycle lane: the
// inbound interfaces the engine implements, the outbound interfaces its adapters
// implement, and the plain DTOs that cross those edges. It holds no logic.
package ports

import (
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

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
