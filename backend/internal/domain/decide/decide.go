// Package decide is the pure DECIDE core: total, deterministic, zero I/O. It
// collapses observed liveness facts (plus the prior detecting memory) into one
// LifecycleDecision. Every function here is side-effect free so the whole
// liveness truth-table can be tested in isolation.
//
// PR-driven behaviour is NOT here: PR display status is derived by
// domain.DeriveStatus from the pr table, and PR-driven nudges are the reaction
// engine's job. decide is only about liveness + the anti-flap quarantine.
package decide

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Anti-flap tuning. detecting escalates to stuck only after this many
// consecutive unchanged-evidence ticks OR once this much wallclock has elapsed
// since first entering detecting.
const (
	DetectingMaxAttempts = 3
	DetectingMaxDuration = 5 * time.Minute
)

// ResolveProbeDecision reconciles runtime/process liveness into a decision.
//
// The ordering encodes the load-bearing invariants:
//   - an explicit kill short-circuits straight to terminal (the only inferred
//     terminal this decider may reach without quarantine);
//   - a *failed* probe (timeout/error) is never read as death — it routes to
//     detecting, as does any disagreement between the two probes;
//   - only runtime-down + process-dead + no-recent-activity reaches terminal.
func ResolveProbeDecision(in ProbeInput) LifecycleDecision {
	if in.KillRequested {
		reason := in.KillReason
		if reason == "" {
			reason = domain.TermManuallyKilled
		}
		return LifecycleDecision{
			Evidence:          "manual kill requested",
			SessionState:      domain.SessionTerminated,
			TerminationReason: reason,
			IsAlive:           false,
		}
	}

	if in.RuntimeFailed || in.ProcessFailed {
		ev := fmt.Sprintf("probe_failed runtimeFailed=%t process=%s processFailed=%t", in.RuntimeFailed, in.Process, in.ProcessFailed)
		return detecting(in, ev)
	}

	if in.RuntimeAlive {
		if in.Process == ProcessDead {
			// Runtime up but the agent process is gone: probes disagree.
			ev := fmt.Sprintf("disagree runtime=alive process=%s recentActivity=%t", in.Process, in.RecentActivity)
			return detecting(in, ev)
		}
		return LifecycleDecision{
			Evidence:     fmt.Sprintf("alive runtime=alive process=%s", in.Process),
			SessionState: domain.SessionWorking,
			IsAlive:      true,
		}
	}

	// Runtime is gone. Death is only concluded when the process is *also*
	// confirmed dead AND nothing has been heard from the agent recently; any
	// other shape is ambiguous and quarantines.
	if in.Process == ProcessAlive || in.RecentActivity {
		ev := fmt.Sprintf("disagree runtime=down process=%s recentActivity=%t", in.Process, in.RecentActivity)
		return detecting(in, ev)
	}
	if in.Process == ProcessDead {
		return LifecycleDecision{
			Evidence:          "dead runtime=down process=dead recentActivity=false",
			SessionState:      domain.SessionTerminated,
			TerminationReason: domain.TermRuntimeLost,
			IsAlive:           false,
		}
	}
	// Process indeterminate: cannot confirm death, so quarantine.
	ev := fmt.Sprintf("runtime_lost runtime=down process=%s recentActivity=false", in.Process)
	return detecting(in, ev)
}

// CreateDetectingDecision advances or escalates the anti-flap quarantine.
//
// The attempt counter climbs only while the (timestamp-stripped) evidence hash
// is unchanged and resets the moment the evidence moves; StartedAt is preserved
// across the whole detecting episode so the duration cap is a real wall-clock
// safety net even when the evidence keeps flapping. Escalation to stuck fires at
// DetectingMaxAttempts consecutive unchanged ticks OR DetectingMaxDuration
// elapsed since first entering detecting. Detecting/stuck leave IsAlive true:
// the probe was ambiguous, so the session is not confirmed dead.
func CreateDetectingDecision(in DetectingInput) LifecycleDecision {
	hash := HashEvidence(in.Evidence)

	attempts := 1
	startedAt := in.Now
	if in.Prior != nil {
		startedAt = in.Prior.StartedAt
		if in.Prior.EvidenceHash == hash {
			attempts = in.Prior.Attempts + 1
		}
	}

	escalate := attempts >= DetectingMaxAttempts || !in.Now.Before(startedAt.Add(DetectingMaxDuration))
	if escalate {
		return LifecycleDecision{
			Evidence:     in.Evidence,
			SessionState: domain.SessionStuck,
			IsAlive:      true,
		}
	}

	return LifecycleDecision{
		Evidence:     in.Evidence,
		Detecting:    &domain.DetectingState{Attempts: attempts, StartedAt: startedAt, EvidenceHash: hash},
		SessionState: domain.SessionDetecting,
		IsAlive:      true,
	}
}

// HashEvidence normalises an evidence string (stripping timestamps and
// collapsing whitespace) and hashes it, so unchanged-but-restamped signals
// compare equal and the detecting counter is not reset by clock movement alone.
func HashEvidence(evidence string) string {
	s := evidence
	for _, re := range timestampPatterns {
		s = re.ReplaceAllString(s, "")
	}
	s = strings.Join(strings.Fields(s), " ")
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// timestampPatterns is the list of regexes HashEvidence applies (in order) to
// delete the time-varying parts of an evidence string before hashing.
var timestampPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2})?`),
	regexp.MustCompile(`\d{2}:\d{2}:\d{2}(?:\.\d+)?`),
	regexp.MustCompile(`\b\d{10,13}\b`),
}

// detecting packages a probe verdict into the shared anti-flap path, so every
// probe-driven ambiguity is counted and escalated by the identical quarantine
// logic instead of each probe branch re-implementing the counter.
func detecting(in ProbeInput, evidence string) LifecycleDecision {
	return CreateDetectingDecision(DetectingInput{
		Evidence: evidence,
		Prior:    in.Prior,
		Now:      in.Now,
	})
}
