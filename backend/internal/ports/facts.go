// Package ports declares the boundary contracts for the LCM + Session Manager
// lane: the inbound interfaces we implement, the outbound interfaces others
// implement for us, and the fact DTOs that cross those boundaries.
//
// These are the types the SCM poller, persistence adapter, and API layer build
// against, so they are committed and stabilised before the LCM/SM logic.
package ports

import (
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// SCMFacts is produced by the SCM poller and handed to ApplySCMObservation.
//
// Fetched is the failed-probe guard: when false, the GitHub query timed out or
// errored and the rest of the struct is meaningless — the LCM must NOT read it
// as "no PR / PR closed" (the SCM analogue of "failed probe != dead").
//
// CIFailureLogTail is a pointer because it is only populated when CI is failing;
// it carries ~120 lines and we don't want it on the hot poll path otherwise.
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

// Mergeability is the structured "can this merge?" answer. CIPassing/Approved
// here overlap CISummary/ReviewDecision by design (different granularity);
// Mergeability is authoritative for the merge gate, the others for display.
type Mergeability struct {
	Mergeable   bool
	CIPassing   bool
	Approved    bool
	NoConflicts bool
	Blockers    []string
}

// ReviewComment carries IsBot so the decider can route bot review comments
// (bugbot-comments reaction) differently from human ones (changes-requested).
type ReviewComment struct {
	Author string
	Body   string
	IsBot  bool
	URL    string
}

// RuntimeFacts is produced by the reaper and handed to ApplyRuntimeObservation.
type RuntimeFacts struct {
	ObservedAt   time.Time
	RuntimeState RuntimeProbe
	ProcessState ProcessProbe
}

// RuntimeProbe / ProcessProbe keep "failed" (the probe call itself errored or
// timed out) distinct from "indeterminate" (the probe ran but couldn't tell) —
// they route differently in the decider.
type RuntimeProbe string

const (
	RuntimeProbeAlive         RuntimeProbe = "alive"
	RuntimeProbeDead          RuntimeProbe = "dead"
	RuntimeProbeIndeterminate RuntimeProbe = "indeterminate"
	RuntimeProbeFailed        RuntimeProbe = "failed"
)

type ProcessProbe string

const (
	ProcessProbeAlive         ProcessProbe = "alive"
	ProcessProbeDead          ProcessProbe = "dead"
	ProcessProbeIndeterminate ProcessProbe = "indeterminate"
	ProcessProbeFailed        ProcessProbe = "failed"
)

// ActivitySignal is pushed by agent hooks / the FS watcher. State is the
// confidence wrapper (so unavailable/probe_failure != idleness); Activity is
// the actual classification.
type ActivitySignal struct {
	State     SignalConfidence
	Activity  domain.ActivityState
	Timestamp time.Time
	Source    domain.ActivitySource
}

type SignalConfidence string

const (
	SignalValid        SignalConfidence = "valid"
	SignalStale        SignalConfidence = "stale"
	SignalNull         SignalConfidence = "null"
	SignalUnavailable  SignalConfidence = "unavailable"
	SignalProbeFailure SignalConfidence = "probe_failure"
)

// SpawnOutcome is what the Session Manager reports to the LCM after a spawn.
// RuntimeHandle is the same structured handle the Runtime port returns, so no
// ad-hoc string encoding is needed for later Destroy/SendMessage calls.
//
// Prompt is the assembled launch prompt persisted as metadata so Restore can
// fall back to a fresh launch (Agent.GetLaunchCommand) when the agent's native
// session id was never captured — without it Restore would have nothing to
// resume and nothing to re-seed a fresh run with.
type SpawnOutcome struct {
	Branch         string
	WorkspacePath  string
	RuntimeHandle  RuntimeHandle
	AgentSessionID string
	Prompt         string
}

// KillReason is what the Session Manager reports to the LCM when a kill is
// requested. Kind drives whether the terminal state is killed/cleanup/errored.
type KillReason struct {
	Kind   LifecycleKillReason
	Detail string
}

type LifecycleKillReason string

const (
	KillManual  LifecycleKillReason = "manual"
	KillCleanup LifecycleKillReason = "cleanup"
	KillError   LifecycleKillReason = "error"
)
