package lifecycle

// reactions.go is the ACT layer: the reaction table, the per-(session,reaction)
// escalation engine, and the duration-driven TickEscalations the synchronous
// LCM can't wake itself for. Reactions fire from react() after a transition is
// persisted by the Apply* pipeline (see manager.go).
//
// Dispatch is synchronous: react() runs Send/Notify inline. It is the single
// dispatch chokepoint, so moving it onto a worker goroutine later (once a daemon
// owns that goroutine's lifecycle) is a change confined to this one function.

import (
	"context"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// reactionKey names a row in the reaction table and a tracker bucket.
type reactionKey string

const (
	reactionCIFailed         reactionKey = "ci-failed"
	reactionChangesRequested reactionKey = "changes-requested"
	reactionBugbotComments   reactionKey = "bugbot-comments"
	reactionMergeConflicts   reactionKey = "merge-conflicts"
	reactionAgentIdle        reactionKey = "agent-idle"
	reactionApprovedAndGreen reactionKey = "approved-and-green"
	reactionAgentStuck       reactionKey = "agent-stuck"
	reactionNeedsInput       reactionKey = "agent-needs-input"
	reactionAgentExited      reactionKey = "agent-exited"
	reactionPRClosed         reactionKey = "pr-closed"
	reactionAllComplete      reactionKey = "all-complete"
)

type actionKind string

const (
	actionSendToAgent actionKind = "send-to-agent"
	actionNotify      actionKind = "notify"
	actionAutoMerge   actionKind = "auto-merge"
)

// reactionConfig is one row of the reaction table (distillation §4.1/§4.2).
//
//   - retries       numeric escalation cap: escalate once attempts exceed it.
//   - escalateAfter  duration escalation: escalate once this elapses since the
//     first attempt (fired by TickEscalations, since the LCM never polls).
//   - persistent     the tracker survives the status leaving the triggering
//     state; it only resets when the incident is truly over (PR no longer open
//     or the session terminal). Only ci-failed is persistent, so a flapping
//     CI (fail→pending→fail) keeps draining one shared retry budget.
type reactionConfig struct {
	action        actionKind
	message       string
	priority      ports.EventPriority
	eventType     string
	retries       int
	escalateAfter time.Duration
	persistent    bool
}

// defaultReactions is the product's default behaviour (distillation §4.2).
// auto-merge is intentionally absent: approved-and-green is a notify, so the
// human decides to merge. The auto-merge action kind exists for opt-in configs,
// but no default row uses it.
var defaultReactions = map[reactionKey]reactionConfig{
	reactionCIFailed: {
		action: actionSendToAgent, persistent: true, retries: 2,
		message:   "CI is failing on your PR. Review the failing output below and push a fix.",
		eventType: "reaction.ci-failed", priority: ports.PriorityAction,
	},
	reactionChangesRequested: {
		action: actionSendToAgent, escalateAfter: 30 * time.Minute,
		message:   "A reviewer requested changes on your PR. Address the comments and push.",
		eventType: "reaction.changes-requested", priority: ports.PriorityAction,
	},
	reactionBugbotComments: {
		action: actionSendToAgent, escalateAfter: 30 * time.Minute,
		message:   "An automated reviewer left comments on your PR. Address them and push.",
		eventType: "reaction.bugbot-comments", priority: ports.PriorityAction,
	},
	reactionMergeConflicts: {
		action: actionSendToAgent, escalateAfter: 15 * time.Minute,
		message:   "Your PR has merge conflicts. Rebase onto the base branch and resolve them.",
		eventType: "reaction.merge-conflicts", priority: ports.PriorityAction,
	},
	reactionAgentIdle: {
		action: actionSendToAgent, retries: 2, escalateAfter: 15 * time.Minute,
		message:   "You appear idle. Continue the task or explain what is blocking you.",
		eventType: "reaction.agent-idle", priority: ports.PriorityWarning,
	},
	reactionApprovedAndGreen: {
		// notify-only: a green, approved PR is the human-decision path — the human
		// decides to merge (no auto-merge by default).
		action: actionNotify, priority: ports.PriorityAction,
		message:   "PR is approved and green — ready to merge.",
		eventType: "reaction.approved-and-green",
	},
	reactionAgentStuck: {
		// §4.2 lists a threshold: 10m here; it is intentionally not gated — entry
		// into stuck is already debounced upstream by the detecting->stuck
		// quarantine (DETECTING_MAX_ATTEMPTS/DURATION), so a second timer would be
		// redundant.
		action: actionNotify, priority: ports.PriorityUrgent,
		message:   "Agent is stuck and needs attention.",
		eventType: "reaction.agent-stuck",
	},
	reactionNeedsInput: {
		action: actionNotify, priority: ports.PriorityUrgent,
		message:   "Agent needs input to continue.",
		eventType: "reaction.agent-needs-input",
	},
	reactionAgentExited: {
		action: actionNotify, priority: ports.PriorityUrgent,
		message:   "Agent process exited unexpectedly.",
		eventType: "reaction.agent-exited",
	},
	reactionPRClosed: {
		action: actionNotify, priority: ports.PriorityAction,
		message:   "PR was closed without merging — decide: resume, learn, or terminate.",
		eventType: "reaction.pr-closed",
	},
	reactionAllComplete: {
		action: actionNotify, priority: ports.PriorityInfo,
		message:   "PR merged — work complete.",
		eventType: "reaction.all-complete",
	},
}

// reactionEventFor maps a canonical record to the reaction it should drive,
// mirroring DeriveLegacyStatus but for the ACT layer. ok is false when the
// current state has no reaction.
//
// A closed PR derives to the idle display status, so it is detected from the PR
// axis directly before falling through to the status mapping. Bot review
// comments and merge conflicts are represented as PR reasons so the ACT layer
// can distinguish them from human-requested changes and plain open PRs.
func reactionEventFor(l domain.CanonicalSessionLifecycle) (reactionKey, bool) {
	if l.PR.State == domain.PRClosed {
		return reactionPRClosed, true
	}
	if isActivePRState(l.PR.State) {
		switch l.PR.Reason {
		case domain.PRReasonBotComments:
			return reactionBugbotComments, true
		case domain.PRReasonMergeConflicts:
			return reactionMergeConflicts, true
		}
	}
	switch domain.DeriveLegacyStatus(l) {
	case domain.StatusCIFailed:
		return reactionCIFailed, true
	case domain.StatusChangesRequested:
		return reactionChangesRequested, true
	case domain.StatusApproved, domain.StatusMergeable:
		return reactionApprovedAndGreen, true
	case domain.StatusIdle:
		return reactionAgentIdle, true
	case domain.StatusStuck:
		return reactionAgentStuck, true
	case domain.StatusNeedsInput:
		return reactionNeedsInput, true
	case domain.StatusKilled:
		// Inferred death only — an explicit user kill goes through
		// OnKillRequested, which does not react.
		return reactionAgentExited, true
	case domain.StatusMerged:
		return reactionAllComplete, true
	}
	return "", false
}

// reactionContext carries fact-derived material the message templates need. The
// SCM path populates it (CI failure log tail); other paths pass the zero value.
type reactionContext struct {
	ciFailureLogTail *string
}

// trackerKey buckets an escalation tracker by session and reaction.
type trackerKey struct {
	id  domain.SessionID
	key reactionKey
}

// reactionTracker is the per-(session,reaction) escalation budget. It lives in
// memory on the Manager: a daemon restart resets budgets, which only ever costs
// a few extra agent retries before re-escalating — never a missed human
// notification. Keeping it out of the canonical store preserves the
// truth-vs-policy split (the store holds session truth; this is ACT policy).
//
// projectID is captured at first attempt so TickEscalations — which fires from
// the reaper and has no transition on hand — can still populate ProjectID on
// the escalation event. It is set once and never overwritten; reaction-bearing
// transitions for a given session id always carry the same projectID.
type reactionTracker struct {
	attempts       int
	escalated      bool
	firstAttemptAt time.Time
	projectID      domain.ProjectID
}

// react fires the ACT layer after a persisted transition: clear the tracker for
// the reaction we left, then dispatch the reaction for the one we entered. It
// fires only on a genuine reaction change, so re-persisting the same state does
// not re-dispatch. Synchronous by design (see file header).
//
// Integration-time caveat: react runs AFTER withLock releases (deliberately, so
// a busy-waiting send-to-agent never holds the per-session mutex). Under a live
// daemon with concurrent observers (SCM poller + reaper + activity ingest) the
// afterLC snapshot can be stale by dispatch time — e.g. a ci-failed send firing
// after the session already moved to approved. Tests are single-threaded so it
// is not observable yet; when the daemon lands, give react a per-session
// ordering (a small react queue) or re-check the triggering state before
// dispatching.
func (m *Manager) react(ctx context.Context, id domain.SessionID, tr *transition, rc reactionContext) error {
	if tr == nil {
		return nil
	}
	beforeKey, hadBefore := reactionEventFor(tr.beforeLC)
	afterKey, hasAfter := reactionEventFor(tr.afterLC)

	changed := beforeKey != afterKey

	switch {
	case incidentOver(tr.afterLC) || recovered(tr.afterLC):
		// The PR-pipeline incident has ended — the PR resolved (merged/closed),
		// the session went terminal, or it reached an approved/green state. Every
		// tracker for this session is now stale, including a persistent ci-failed
		// one. This is keyed on the state REACHED, not the one left: the recovery
		// transition is typically review_pending->approved (beforeKey empty), so
		// clearing only beforeKey would leak the ci-failed tracker and leave its
		// escalated=true to silence a future regression. Clear them all.
		m.clearSessionTrackers(id)
	case hadBefore && (!hasAfter || changed):
		// Within an unresolved open PR: a normal tracker resets when its state is
		// left. A persistent one (ci-failed) is NOT cleared here — it must survive
		// the ambiguous review_pending limbo (the fail->pending->fail flap, §4.2);
		// it only resets via the recovery/incident-over branch above.
		if !defaultReactions[beforeKey].persistent {
			m.clearTracker(id, beforeKey)
		}
	}

	if hasAfter && (!hadBefore || changed) {
		return m.executeReaction(ctx, id, tr.projectID, afterKey, rc)
	}
	return nil
}

// incidentOver reports that a PR-pipeline incident has truly ended (PR no longer
// active, or the session terminal), so all trackers for the session may reset.
func incidentOver(l domain.CanonicalSessionLifecycle) bool {
	return !isActivePRState(l.PR.State) || isTerminal(l.Session.State)
}

func isActivePRState(s domain.PRState) bool {
	return s == domain.PROpen || s == domain.PRDraft
}

// recovered reports a genuinely-green open PR: an approved/mergeable state, which
// unambiguously means CI is no longer failing (the open-PR ladder ranks ci_failing
// above approved, so an approved display cannot coexist with failing CI). Unlike
// the ambiguous review_pending state — which may just be CI re-running — reaching
// this ends a ci-failed incident and re-arms its budget. Draft PRs are active,
// but not recoverable via review/merge state.
func recovered(l domain.CanonicalSessionLifecycle) bool {
	if !isActivePRState(l.PR.State) || l.PR.State == domain.PRDraft {
		return false
	}
	switch l.PR.Reason {
	case domain.PRReasonApproved, domain.PRReasonMergeReady:
		return true
	default:
		return false
	}
}

func (m *Manager) executeReaction(ctx context.Context, id domain.SessionID, projectID domain.ProjectID, key reactionKey, rc reactionContext) error {
	cfg := defaultReactions[key]
	switch cfg.action {
	case actionNotify:
		// notify reactions are human-attention terminals: fire once on the
		// triggering transition, no retry/escalation budget.
		return m.notifier.Notify(ctx, ports.OrchestratorEvent{
			Type:      cfg.eventType,
			Priority:  cfg.priority,
			SessionID: id,
			ProjectID: projectID,
			Message:   cfg.message,
		})
	case actionAutoMerge:
		// Off by default: no default row maps here, and wiring a merge port is a
		// later PR. An opt-in config could route a reaction here.
		return nil
	case actionSendToAgent:
		return m.sendToAgent(ctx, id, projectID, key, cfg, rc)
	}
	return nil
}

// sendToAgent runs the escalation engine for an auto send-to-agent reaction:
// count the attempt, escalate when the numeric cap or duration is exceeded
// (silencing further auto-dispatch), else inject the message via the messenger.
func (m *Manager) sendToAgent(ctx context.Context, id domain.SessionID, projectID domain.ProjectID, key reactionKey, cfg reactionConfig, rc reactionContext) error {
	m.trackerMu.Lock()
	tk := m.trackerFor(id, key)
	// Capture projectID once so the duration-based TickEscalations path — which
	// has no transition on hand — can still populate ProjectID on the escalation
	// event. A non-empty incoming projectID always wins, in case the tracker was
	// first created from an observation that lacked one.
	if projectID != "" {
		tk.projectID = projectID
	}
	if tk.escalated {
		m.trackerMu.Unlock()
		return nil // silenced until the condition clears the tracker
	}
	now := m.clock()
	freshFirst := tk.firstAttemptAt.IsZero()
	if freshFirst {
		tk.firstAttemptAt = now
	}
	tk.attempts++
	if shouldEscalate(tk, cfg, now) {
		tk.escalated = true
		m.trackerMu.Unlock()
		return m.escalate(ctx, id, tk.projectID, key)
	}
	m.trackerMu.Unlock()

	if err := m.messenger.Send(ctx, id, composeMessage(cfg, rc)); err != nil {
		// A delivery failure must not consume escalation budget: roll this
		// attempt back so the next relevant transition retries from the same
		// point rather than marching toward escalation on undelivered messages
		// (distillation §4.3).
		m.trackerMu.Lock()
		tk.attempts--
		if freshFirst {
			tk.firstAttemptAt = time.Time{}
		}
		m.trackerMu.Unlock()
		return err
	}
	return nil
}

// shouldEscalate uses inclusive boundaries: escalate once the numeric cap is
// exceeded or once exactly escalateAfter has elapsed (don't wait for the next
// tick to cross a strict threshold).
func shouldEscalate(tk *reactionTracker, cfg reactionConfig, now time.Time) bool {
	if cfg.retries > 0 && tk.attempts > cfg.retries {
		return true
	}
	if cfg.escalateAfter > 0 && !tk.firstAttemptAt.IsZero() && now.Sub(tk.firstAttemptAt) >= cfg.escalateAfter {
		return true
	}
	return false
}

// escalate emits reaction.escalated and notifies the human. The caller has
// already set tracker.escalated under the lock, which silences further
// auto-dispatch for this reaction until the tracker clears.
func (m *Manager) escalate(ctx context.Context, id domain.SessionID, projectID domain.ProjectID, key reactionKey) error {
	return m.notifier.Notify(ctx, ports.OrchestratorEvent{
		Type:      "reaction.escalated",
		Priority:  ports.PriorityUrgent,
		SessionID: id,
		ProjectID: projectID,
		Message:   fmt.Sprintf("auto-handling of %q is exhausted and needs a human.", key),
		Data:      map[string]any{"reaction": string(key)},
	})
}

func composeMessage(cfg reactionConfig, rc reactionContext) string {
	if rc.ciFailureLogTail != nil && *rc.ciFailureLogTail != "" {
		return cfg.message + "\n\nFailing output:\n" + *rc.ciFailureLogTail
	}
	return cfg.message
}

// trackerFor returns the tracker for (id,key), creating it on first use. The
// caller must hold trackerMu.
func (m *Manager) trackerFor(id domain.SessionID, key reactionKey) *reactionTracker {
	k := trackerKey{id: id, key: key}
	tk := m.trackers[k]
	if tk == nil {
		tk = &reactionTracker{}
		m.trackers[k] = tk
	}
	return tk
}

func (m *Manager) clearTracker(id domain.SessionID, key reactionKey) {
	m.trackerMu.Lock()
	delete(m.trackers, trackerKey{id: id, key: key})
	m.trackerMu.Unlock()
}

// clearSessionTrackers drops every tracker for a session — used when its
// incident is over, so no budget (and no stale escalated=true) survives into a
// later unrelated incident.
func (m *Manager) clearSessionTrackers(id domain.SessionID) {
	m.trackerMu.Lock()
	for k := range m.trackers {
		if k.id == id {
			delete(m.trackers, k)
		}
	}
	m.trackerMu.Unlock()
}

// TickEscalations fires the duration-based escalations the synchronous LCM
// cannot wake itself for. The reaper calls it on a timer; it escalates any
// not-yet-escalated tracker whose escalateAfter has elapsed. Notifications are
// sent outside the lock so agent/notifier latency never blocks tracker access.
func (m *Manager) TickEscalations(ctx context.Context, now time.Time) error {
	type due struct {
		id        domain.SessionID
		projectID domain.ProjectID
		key       reactionKey
	}
	var fire []due

	m.trackerMu.Lock()
	for k, tk := range m.trackers {
		if tk.escalated {
			continue
		}
		cfg := defaultReactions[k.key]
		if cfg.escalateAfter > 0 && !tk.firstAttemptAt.IsZero() && now.Sub(tk.firstAttemptAt) >= cfg.escalateAfter {
			tk.escalated = true
			fire = append(fire, due{id: k.id, projectID: tk.projectID, key: k.key})
		}
	}
	m.trackerMu.Unlock()

	for _, d := range fire {
		if err := m.escalate(ctx, d.id, d.projectID, d.key); err != nil {
			return err
		}
	}
	return nil
}
