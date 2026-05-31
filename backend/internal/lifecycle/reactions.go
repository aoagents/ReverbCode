package lifecycle

// reactions.go is the ACT layer: after a persisted transition the engine maps
// the session's (state, PR facts) to at most one reaction and dispatches it —
// nudging the agent or paging the human. Two reactions inject live content (CI
// logs, review comments) and re-fire when that content changes; the rest fire
// once on entry, with duration escalation driven by TickEscalations.
//
// Budgets are in-memory: a restart re-arms them, which costs a few extra nudges,
// never a missed page.

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type reactionKey string

const (
	rxCIFailed       reactionKey = "ci-failed"
	rxReviewComments reactionKey = "review-comments"
	rxMergeConflicts reactionKey = "merge-conflicts"
	rxIdle           reactionKey = "agent-idle"
	rxApprovedGreen  reactionKey = "approved-and-green"
	rxStuck          reactionKey = "agent-stuck"
	rxNeedsInput     reactionKey = "agent-needs-input"
	rxExited         reactionKey = "agent-exited"
	rxPRClosed       reactionKey = "pr-closed"
	rxMerged         reactionKey = "pr-merged"
)

// Brakes: stop auto-handling and page a human after this many failed attempts.
const (
	ciBrakeRuns    = 3 // last N runs of a failing check all failed
	reviewMaxNudge = 3 // re-nudged the agent N times over new review feedback
)

// reactionConfig is one row of the reaction table. toAgent reactions nudge the
// agent; the rest notify the human. escalateAfter (when set) drives a
// duration-based escalation via TickEscalations.
type reactionConfig struct {
	toAgent       bool
	message       string
	eventType     string
	priority      ports.Priority
	escalateAfter time.Duration
}

var reactions = map[reactionKey]reactionConfig{
	rxCIFailed:       {toAgent: true, eventType: "reaction.ci-failed", priority: ports.PriorityAction, message: "CI is failing on your PR. Review the output below and push a fix."},
	rxReviewComments: {toAgent: true, eventType: "reaction.review-comments", priority: ports.PriorityAction, message: "A reviewer left feedback on your PR. Address it and push."},
	rxMergeConflicts: {toAgent: true, eventType: "reaction.merge-conflicts", priority: ports.PriorityAction, escalateAfter: 15 * time.Minute, message: "Your PR has merge conflicts. Rebase onto the base branch and resolve them."},
	rxIdle:           {toAgent: true, eventType: "reaction.agent-idle", priority: ports.PriorityInfo, escalateAfter: 15 * time.Minute, message: "You appear idle. Continue the task or say what is blocking you."},
	rxApprovedGreen:  {eventType: "reaction.approved-and-green", priority: ports.PriorityAction, message: "PR is approved and green — ready to merge."},
	rxStuck:          {eventType: "reaction.agent-stuck", priority: ports.PriorityUrgent, message: "Agent is stuck and needs attention."},
	rxNeedsInput:     {eventType: "reaction.agent-needs-input", priority: ports.PriorityUrgent, message: "Agent needs input to continue."},
	rxExited:         {eventType: "reaction.agent-exited", priority: ports.PriorityUrgent, message: "Agent process exited unexpectedly."},
	rxPRClosed:       {eventType: "reaction.pr-closed", priority: ports.PriorityAction, message: "PR was closed without merging."},
	rxMerged:         {eventType: "reaction.pr-merged", priority: ports.PriorityInfo, message: "PR merged — work complete."},
}

// reactionContent carries the live material the feedback reactions inject. Empty
// for runtime/activity transitions; populated from a PR observation.
type reactionContent struct {
	ciCheck   string
	ciCommit  string
	ciURL     string
	ciLogTail string
	comments  []string
	reviewSig string
}

// prContent extracts the CI failure + review feedback from a PR observation.
func prContent(o ports.PRObservation) reactionContent {
	c := reactionContent{}
	for _, ch := range o.Checks {
		if ch.Status == "failed" {
			c.ciCheck, c.ciCommit, c.ciLogTail, c.ciURL = ch.Name, ch.CommitHash, ch.LogTail, o.URL
			break
		}
	}
	var ids []string
	for _, cm := range o.Comments {
		if cm.Resolved {
			continue
		}
		c.comments = append(c.comments, cm.Body)
		ids = append(ids, cm.ID)
	}
	c.reviewSig = strings.Join(ids, ",")
	return c
}

// ---- in-memory escalation state ----

type trackerKey struct {
	id  domain.SessionID
	key reactionKey
}

type tracker struct {
	attempts  int
	firstAt   time.Time
	escalated bool
	seenSig   bool
	lastSig   string
	projectID domain.ProjectID
}

type reactionState struct {
	mu       sync.Mutex
	trackers map[trackerKey]*tracker
	lastKey  map[domain.SessionID]reactionKey
}

func newReactionState() reactionState {
	return reactionState{trackers: map[trackerKey]*tracker{}, lastKey: map[domain.SessionID]reactionKey{}}
}

// trackerFor returns the (id,key) tracker, creating it on first use. Caller holds mu.
func (rs *reactionState) trackerFor(id domain.SessionID, key reactionKey) *tracker {
	k := trackerKey{id, key}
	t := rs.trackers[k]
	if t == nil {
		t = &tracker{}
		rs.trackers[k] = t
	}
	return t
}

func (m *Manager) clearReactions(id domain.SessionID) {
	m.react.mu.Lock()
	defer m.react.mu.Unlock()
	for k := range m.react.trackers {
		if k.id == id {
			delete(m.react.trackers, k)
		}
	}
	delete(m.react.lastKey, id)
}

// ---- dispatch ----

// runReactions is the chokepoint called after every persisted transition. It
// runs unlocked (the write lock is already released) so a busy agent send never
// blocks the write path.
func (m *Manager) runReactions(ctx context.Context, id domain.SessionID, content reactionContent) error {
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil || !ok {
		return err
	}
	lc := rec.Lifecycle
	project := rec.ProjectID

	if isTerminal(lc.Session.State) {
		err := m.dispatch(ctx, id, project, terminalReaction(lc.TerminationReason))
		m.clearReactions(id) // incident over: drop budgets after the final notify
		return err
	}

	pr, err := m.store.PRFactsForSession(ctx, id)
	if err != nil {
		return err
	}

	// Feedback reactions inject live content and re-fire as it changes — only
	// while the agent can actually act on it.
	if pr.Exists && !pr.Closed && !needsHuman(lc.Session.State) {
		if pr.CI == domain.CIFailing && content.ciCheck != "" {
			if err := m.handleCIFailure(ctx, id, project, content); err != nil {
				return err
			}
		}
		if hasReviewFeedback(pr) {
			if err := m.handleReviewFeedback(ctx, id, project, content); err != nil {
				return err
			}
		}
	}

	return m.dispatch(ctx, id, project, reactionFor(lc, pr))
}

// dispatch fires the entry reaction for key, deduped so a steady state does not
// re-fire. Leaving a reaction drops its budget.
func (m *Manager) dispatch(ctx context.Context, id domain.SessionID, project domain.ProjectID, key reactionKey) error {
	m.react.mu.Lock()
	if m.react.lastKey[id] == key {
		m.react.mu.Unlock()
		return nil
	}
	if prev := m.react.lastKey[id]; prev != "" {
		delete(m.react.trackers, trackerKey{id, prev})
	}
	m.react.lastKey[id] = key
	m.react.mu.Unlock()

	if key == "" {
		return nil
	}
	cfg := reactions[key]
	if cfg.toAgent {
		return m.fireAgentEntry(ctx, id, project, key, cfg)
	}
	return m.fireNotify(ctx, id, project, cfg)
}

// reactionFor maps (session state, PR facts) to the reaction to enter. CI failure
// and review feedback return "" here — they are handled by the feedback path.
func reactionFor(lc domain.CanonicalSessionLifecycle, pr domain.PRFacts) reactionKey {
	switch lc.Session.State {
	case domain.SessionStuck:
		return rxStuck
	case domain.SessionNeedsInput:
		return rxNeedsInput
	}
	if pr.Exists {
		if pr.Closed {
			if !pr.Merged {
				return rxPRClosed
			}
			return ""
		}
		switch {
		case pr.CI == domain.CIFailing, hasReviewFeedback(pr):
			return "" // feedback path
		case pr.Mergeability == domain.MergeConflicting:
			return rxMergeConflicts
		case pr.Mergeability == domain.MergeMergeable, pr.Review == domain.ReviewApproved:
			return rxApprovedGreen
		}
	}
	if lc.Session.State == domain.SessionIdle {
		return rxIdle
	}
	return ""
}

func hasReviewFeedback(pr domain.PRFacts) bool {
	return pr.Review == domain.ReviewChangesRequest || pr.ReviewComments
}

func needsHuman(s domain.SessionState) bool {
	return s == domain.SessionStuck || s == domain.SessionNeedsInput
}

// terminalReaction is the notify fired when a session reaches a terminal state by
// inferred death. An explicit kill goes through OnKillRequested (no reaction);
// auto_cleanup / pr_merged are notified elsewhere.
func terminalReaction(r domain.TerminationReason) reactionKey {
	switch r {
	case domain.TermRuntimeLost, domain.TermAgentProcessExited, domain.TermProbeFailure, domain.TermErrorInProcess:
		return rxExited
	default:
		return ""
	}
}

// ---- feedback reactions (content-driven re-fire + brake) ----

func (m *Manager) handleCIFailure(ctx context.Context, id domain.SessionID, project domain.ProjectID, c reactionContent) error {
	msg := reactions[rxCIFailed].message + "\n\nFailing output:\n" + c.ciLogTail
	return m.fireFeedback(ctx, id, project, rxCIFailed, c.ciCommit, msg, func(int) (bool, error) {
		st, err := m.pr.RecentCheckStatuses(ctx, c.ciURL, c.ciCheck, ciBrakeRuns)
		if err != nil {
			return false, err
		}
		return allFailed(st, ciBrakeRuns), nil
	})
}

func (m *Manager) handleReviewFeedback(ctx context.Context, id domain.SessionID, project domain.ProjectID, c reactionContent) error {
	msg := reactions[rxReviewComments].message
	if len(c.comments) > 0 {
		msg += "\n\n" + strings.Join(c.comments, "\n\n")
	}
	return m.fireFeedback(ctx, id, project, rxReviewComments, c.reviewSig, msg, func(attempts int) (bool, error) {
		return attempts > reviewMaxNudge, nil
	})
}

// fireFeedback nudges the agent with fresh content, deduped by signature so the
// same content is not re-sent each poll. braked decides whether to escalate to a
// human instead (CI: history; review: attempt count).
func (m *Manager) fireFeedback(ctx context.Context, id domain.SessionID, project domain.ProjectID, key reactionKey, sig, message string, braked func(attempts int) (bool, error)) error {
	m.react.mu.Lock()
	t := m.react.trackerFor(id, key)
	if project != "" {
		t.projectID = project
	}
	if t.escalated || (t.seenSig && t.lastSig == sig) {
		m.react.mu.Unlock()
		return nil
	}
	t.seenSig, t.lastSig = true, sig
	t.attempts++
	attempts, pid := t.attempts, t.projectID
	m.react.lastKey[id] = key // feedback owns the slot so a later dispatch("") clears it
	m.react.mu.Unlock()

	brake, err := braked(attempts)
	if err != nil {
		return err
	}
	if brake {
		m.react.mu.Lock()
		t.escalated = true
		m.react.mu.Unlock()
		return m.escalate(ctx, id, pid, key)
	}
	return m.messenger.Send(ctx, id, message)
}

// ---- entry reactions ----

// fireAgentEntry nudges the agent once on entry into a static reaction
// (idle/merge-conflicts); escalation is duration-based via TickEscalations.
func (m *Manager) fireAgentEntry(ctx context.Context, id domain.SessionID, project domain.ProjectID, key reactionKey, cfg reactionConfig) error {
	m.react.mu.Lock()
	t := m.react.trackerFor(id, key)
	if project != "" {
		t.projectID = project
	}
	if t.escalated {
		m.react.mu.Unlock()
		return nil
	}
	if t.firstAt.IsZero() {
		t.firstAt = m.clock()
	}
	t.attempts++
	m.react.mu.Unlock()
	return m.messenger.Send(ctx, id, cfg.message)
}

func (m *Manager) fireNotify(ctx context.Context, id domain.SessionID, project domain.ProjectID, cfg reactionConfig) error {
	return m.notifier.Notify(ctx, ports.Event{
		Type: cfg.eventType, Priority: cfg.priority,
		SessionID: id, ProjectID: project, Message: cfg.message,
	})
}

func (m *Manager) escalate(ctx context.Context, id domain.SessionID, project domain.ProjectID, key reactionKey) error {
	return m.notifier.Notify(ctx, ports.Event{
		Type: "reaction.escalated", Priority: ports.PriorityUrgent,
		SessionID: id, ProjectID: project,
		Message: fmt.Sprintf("Automatic handling of %q is exhausted — needs a human.", key),
	})
}

// TickEscalations fires the duration-based escalations the synchronous engine
// cannot wake itself for. The reaper calls it on a timer.
func (m *Manager) TickEscalations(ctx context.Context, now time.Time) error {
	type due struct {
		id      domain.SessionID
		project domain.ProjectID
		key     reactionKey
	}
	var fire []due
	m.react.mu.Lock()
	for k, t := range m.react.trackers {
		if t.escalated {
			continue
		}
		cfg := reactions[k.key]
		if cfg.escalateAfter > 0 && !t.firstAt.IsZero() && now.Sub(t.firstAt) >= cfg.escalateAfter {
			t.escalated = true
			fire = append(fire, due{k.id, t.projectID, k.key})
		}
	}
	m.react.mu.Unlock()

	for _, d := range fire {
		if err := m.escalate(ctx, d.id, d.project, d.key); err != nil {
			return err
		}
	}
	return nil
}

func allFailed(statuses []string, n int) bool {
	if len(statuses) < n {
		return false
	}
	for i := 0; i < n; i++ {
		if statuses[i] != "failed" {
			return false
		}
	}
	return true
}
