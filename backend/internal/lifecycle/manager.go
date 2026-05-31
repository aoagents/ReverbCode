// Package lifecycle implements ports.LifecycleManager: the synchronous
// observe -> decide -> persist reducer. Every Apply*/On* entrypoint loads the
// session, runs the pure decider, and persists the full row under a single write
// lock. The DB triggers emit the CDC; the engine never writes the change log.
// After a transition it fires the mapped reaction (see reactions.go).
package lifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain/decide"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Manager is the lifecycle engine. mu serialises the load->decide->persist
// read-modify-write across sessions; reactions dispatch after the lock releases
// so a slow agent send never blocks the write path.
type Manager struct {
	store     ports.SessionStore
	pr        ports.PRWriter
	notifier  ports.Notifier
	messenger ports.AgentMessenger

	mu     sync.Mutex
	window time.Duration
	clock  func() time.Time

	// in-memory ACT state (policy, not canonical truth — reset on restart).
	react reactionState
}

var _ ports.LifecycleManager = (*Manager)(nil)

func New(store ports.SessionStore, pr ports.PRWriter, notifier ports.Notifier, messenger ports.AgentMessenger) *Manager {
	return &Manager{
		store:     store,
		pr:        pr,
		notifier:  notifier,
		messenger: messenger,
		window:    defaultRecentActivityWindow,
		clock:     time.Now,
		react:     newReactionState(),
	}
}

// mutate runs the shared pipeline: load -> decideFn -> persist (only if changed).
// It returns whether a write happened. A stray observation for an unknown session
// is a clean no-op.
func (m *Manager) mutate(
	ctx context.Context,
	id domain.SessionID,
	fn func(cur domain.CanonicalSessionLifecycle) (domain.CanonicalSessionLifecycle, bool),
) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil || !ok {
		return false, err
	}
	next, changed := fn(rec.Lifecycle)
	if !changed {
		return false, nil
	}
	next.Version = domain.LifecycleVersion
	rec.Lifecycle = next
	rec.UpdatedAt = m.clock()
	if err := m.store.UpdateSession(ctx, rec); err != nil {
		return false, err
	}
	return true, nil
}

// ---- OBSERVE entrypoints ----

// ApplyRuntimeObservation feeds the probe decider. is_alive always tracks the
// verdict; the session state follows the runtime-write rule; a non-detecting
// verdict clears stale detecting memory.
func (m *Manager) ApplyRuntimeObservation(ctx context.Context, id domain.SessionID, f ports.RuntimeFacts) error {
	changed, err := m.mutate(ctx, id, func(cur domain.CanonicalSessionLifecycle) (domain.CanonicalSessionLifecycle, bool) {
		d := decide.ResolveProbeDecision(probeInput(f, cur, m.window))
		next := cur
		ch := false
		if next.IsAlive != d.IsAlive {
			next.IsAlive, ch = d.IsAlive, true
		}
		if !isTerminal(cur.Session.State) {
			if writeRuntimeSession(d, cur) {
				ch = setSessionState(&next, d.SessionState, d.TerminationReason) || ch
			}
			ch = setDetecting(&next, d.Detecting) || ch
		}
		return next, ch
	})
	if err != nil || !changed {
		return err
	}
	return m.runReactions(ctx, id, reactionContent{})
}

// ApplyActivitySignal updates the activity axis. Only a valid signal is
// authoritative, and it is proof of life: it may resolve a detecting session and
// move the session out of any non-terminal state.
func (m *Manager) ApplyActivitySignal(ctx context.Context, id domain.SessionID, s ports.ActivitySignal) error {
	if !s.Valid {
		return nil
	}
	changed, err := m.mutate(ctx, id, func(cur domain.CanonicalSessionLifecycle) (domain.CanonicalSessionLifecycle, bool) {
		if isTerminal(cur.Session.State) {
			return cur, false
		}
		next := cur
		ch := false
		act := domain.ActivitySubstate{State: s.State, LastActivityAt: nowOr(s.Timestamp), Source: s.Source}
		if !sameActivity(cur.Activity, act) {
			next.Activity, ch = act, true
		}
		if st, ok := activityToSession(s.State); ok {
			ch = setSessionState(&next, st, domain.TermNone) || ch
			if next.Detecting != nil {
				next.Detecting, ch = nil, true
			}
		}
		if s.State != domain.ActivityExited && !next.IsAlive {
			next.IsAlive, ch = true, true
		}
		return next, ch
	})
	if err != nil || !changed {
		return err
	}
	return m.runReactions(ctx, id, reactionContent{})
}

// ApplySCMObservation accepts the normalized SCM observer DTO and projects it
// onto the existing PR observation lane. The SCM snapshot/store owns durable SCM
// truth; the LCM consumes only the PR facts needed for canonical decisions and
// reactions.
func (m *Manager) ApplySCMObservation(ctx context.Context, id domain.SessionID, f ports.SCMFacts) error {
	if !f.Fetched {
		return nil
	}
	o, ok := scmFactsToPRObservation(f)
	if !ok {
		return nil
	}
	return m.ApplyPRObservation(ctx, id, o)
}

// ApplyPRObservation records the observed PR facts in the pr tables, terminates
// the session on a merge, and fires the PR-driven reactions. A failed fetch is
// dropped (failed probe != "PR closed").
func (m *Manager) ApplyPRObservation(ctx context.Context, id domain.SessionID, o ports.PRObservation) error {
	if !o.Fetched {
		return nil
	}
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil || !ok {
		return err
	}
	if err := m.writePR(ctx, id, o); err != nil {
		return err
	}

	if o.Merged {
		changed, err := m.mutate(ctx, id, func(cur domain.CanonicalSessionLifecycle) (domain.CanonicalSessionLifecycle, bool) {
			if isTerminal(cur.Session.State) {
				return cur, false
			}
			next := cur
			next.Session.State = domain.SessionTerminated
			next.TerminationReason = domain.TermPRMerged
			next.IsAlive = false
			next.Detecting = nil
			return next, true
		})
		if err != nil {
			return err
		}
		if changed {
			m.clearReactions(id)
			return m.fireNotify(ctx, id, rec.ProjectID, rxMerged, reactions[rxMerged])
		}
		return nil
	}

	return m.runReactions(ctx, id, prContent(o))
}

// writePR persists the observation's scalar facts, check runs, and comment set
// in one atomic store call. PR-table CDC is emitted by the DB triggers.
func (m *Manager) writePR(ctx context.Context, id domain.SessionID, o ports.PRObservation) error {
	now := m.clock()
	row := ports.PRRow{
		URL: o.URL, SessionID: string(id), Number: o.Number,
		Draft: o.Draft, Merged: o.Merged, Closed: o.Closed,
		CI: o.CI, Review: o.Review, Mergeability: o.Mergeability, UpdatedAt: now,
	}
	checks := make([]ports.PRCheckRow, len(o.Checks))
	for i, c := range o.Checks {
		c.PRURL = o.URL
		if c.CreatedAt.IsZero() {
			c.CreatedAt = now
		}
		checks[i] = c
	}
	comments := make([]ports.PRComment, len(o.Comments))
	for i, c := range o.Comments {
		if c.CreatedAt.IsZero() {
			c.CreatedAt = now
		}
		comments[i] = c
	}
	return m.pr.WritePR(ctx, row, checks, comments)
}

func scmFactsToPRObservation(f ports.SCMFacts) (ports.PRObservation, bool) {
	if f.PRState == "" || f.PRState == domain.PRNone {
		return ports.PRObservation{}, false
	}
	o := ports.PRObservation{
		Fetched:      true,
		URL:          f.PRURL,
		Number:       f.PRNumber,
		Draft:        f.Draft || f.PRState == domain.PRDraft,
		Merged:       f.PRState == domain.PRMerged,
		Closed:       f.PRState == domain.PRClosed || f.PRState == domain.PRMerged,
		CI:           scmCIState(f.CISummary),
		Review:       scmReviewDecision(f.ReviewDecision),
		Mergeability: scmMergeability(f.Mergeability),
	}
	if o.URL == "" && o.Number == 0 {
		return ports.PRObservation{}, false
	}
	for _, c := range f.CIFailedChecks {
		o.Checks = append(o.Checks, ports.PRCheckRow{
			Name:    firstNonEmpty(c.Name, "check"),
			Status:  prCheckStatus(c),
			URL:     c.URL,
			LogTail: firstNonEmpty(c.LogTail, c.Details),
		})
	}
	if f.CIFailureLogTail != nil && *f.CIFailureLogTail != "" && len(o.Checks) == 0 && o.CI == domain.CIFailing {
		o.Checks = append(o.Checks, ports.PRCheckRow{Name: "ci", Status: "failed", LogTail: *f.CIFailureLogTail})
	}
	for _, c := range f.PendingComments {
		o.Comments = append(o.Comments, ports.PRComment{
			ID:       reviewCommentID(c),
			Author:   c.Author,
			File:     c.Path,
			Line:     c.Line,
			Body:     c.Body,
			Resolved: false,
		})
	}
	return o, true
}

func scmCIState(s ports.CISummary) domain.CIState {
	switch s {
	case ports.CIFailing:
		return domain.CIFailing
	case ports.CIPassing:
		return domain.CIPassing
	case ports.CIPending:
		return domain.CIPending
	default:
		return domain.CIUnknown
	}
}

func scmReviewDecision(d ports.ReviewDecision) domain.ReviewDecision {
	switch d {
	case ports.ReviewApproved:
		return domain.ReviewApproved
	case ports.ReviewChangesRequested:
		return domain.ReviewChangesRequest
	case ports.ReviewPending:
		return domain.ReviewRequired
	default:
		return domain.ReviewNone
	}
}

func scmMergeability(m ports.Mergeability) domain.Mergeability {
	switch {
	case m.Unknown:
		return domain.MergeUnknown
	case m.Conflict:
		return domain.MergeConflicting
	case m.Mergeable:
		return domain.MergeMergeable
	case m.BehindBase || len(m.Blockers) > 0:
		return domain.MergeBlocked
	default:
		return domain.MergeUnknown
	}
}

func prCheckStatus(c ports.CICheck) string {
	v := strings.ToLower(strings.TrimSpace(firstNonEmpty(c.Conclusion, c.Status)))
	switch v {
	case "success", "successful", "passed", "passing", "pass":
		return "passed"
	case "failure", "failed", "failing", "error", "timed_out", "cancelled", "action_required", "startup_failure":
		return "failed"
	case "queued", "requested", "waiting", "expected":
		return "queued"
	case "in_progress", "pending":
		return "in_progress"
	case "skipped", "neutral", "stale":
		return "skipped"
	default:
		if v == "" {
			return "unknown"
		}
		return v
	}
}

func reviewCommentID(c ports.ReviewComment) string {
	if c.ThreadID != "" && c.URL != "" {
		return c.ThreadID + "|" + c.URL
	}
	seed := fmt.Sprintf("%s|%s|%s|%d|%s|%s", c.ThreadID, c.URL, c.Author, c.Line, c.Path, c.Body)
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:])
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// ---- mutation commands from the Session Manager ----

// OnSpawnCompleted marks a session live and folds in its handles. It serves a
// fresh spawn (not_started -> live) and a restore (terminal -> reopened): both
// land at not_started + is_alive, with the agent acknowledging via first activity.
func (m *Manager) OnSpawnCompleted(ctx context.Context, id domain.SessionID, o ports.SpawnOutcome) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("lifecycle: OnSpawnCompleted for unknown session %q", id)
	}
	rec.Lifecycle.Version = domain.LifecycleVersion
	rec.Lifecycle.Session.State = domain.SessionNotStarted
	rec.Lifecycle.TerminationReason = domain.TermNone
	rec.Lifecycle.IsAlive = true
	rec.Lifecycle.Detecting = nil
	rec.Metadata = mergeMetadata(rec.Metadata, spawnMetadata(o))
	rec.UpdatedAt = m.clock()
	return m.store.UpdateSession(ctx, rec)
}

// OnKillRequested is the explicit terminal-write path (the one terminal that does
// not go through the inferred-death decider). It fires no reaction — an explicit
// kill is a human action — but drops the session's ACT state.
func (m *Manager) OnKillRequested(ctx context.Context, id domain.SessionID, reason domain.TerminationReason) error {
	_, err := m.mutate(ctx, id, func(cur domain.CanonicalSessionLifecycle) (domain.CanonicalSessionLifecycle, bool) {
		if isTerminal(cur.Session.State) {
			return cur, false
		}
		if reason == domain.TermNone {
			reason = domain.TermManuallyKilled
		}
		next := cur
		next.Session.State = domain.SessionTerminated
		next.TerminationReason = reason
		next.IsAlive = false
		next.Detecting = nil
		return next, true
	})
	m.clearReactions(id)
	return err
}

// RunningSessions snapshots every non-terminal session for the reaper to probe.
// Detecting sessions are included — a fresh probe is the only fact that recovers
// or escalates them.
func (m *Manager) RunningSessions(ctx context.Context) ([]domain.SessionRecord, error) {
	all, err := m.store.ListAllSessions(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]domain.SessionRecord, 0, len(all))
	for _, rec := range all {
		if !isTerminal(rec.Lifecycle.Session.State) {
			out = append(out, rec)
		}
	}
	return out, nil
}

// ---- diff + metadata helpers ----

// setSessionState sets the state (and, for a terminal state, the reason) when it
// differs. An empty state means "decider doesn't address the session axis".
func setSessionState(next *domain.CanonicalSessionLifecycle, st domain.SessionState, reason domain.TerminationReason) bool {
	if st == "" {
		return false
	}
	changed := false
	if next.Session.State != st {
		next.Session.State, changed = st, true
	}
	want := domain.TermNone
	if st == domain.SessionTerminated {
		want = reason
	}
	if next.TerminationReason != want {
		next.TerminationReason, changed = want, true
	}
	return changed
}

func setDetecting(next *domain.CanonicalSessionLifecycle, d *domain.DetectingState) bool {
	if d != nil {
		if next.Detecting != nil && *next.Detecting == *d {
			return false
		}
		dc := *d
		next.Detecting = &dc
		return true
	}
	if next.Detecting != nil {
		next.Detecting = nil
		return true
	}
	return false
}

// sameActivity compares with time-aware equality (== on time.Time is
// monotonic-clock sensitive and would spuriously report changes).
func sameActivity(a, b domain.ActivitySubstate) bool {
	return a.State == b.State && a.Source == b.Source && a.LastActivityAt.Equal(b.LastActivityAt)
}

func spawnMetadata(o ports.SpawnOutcome) domain.SessionMetadata {
	return domain.SessionMetadata{
		Branch:          o.Branch,
		WorkspacePath:   o.WorkspacePath,
		RuntimeHandleID: o.RuntimeHandle.ID,
		RuntimeName:     o.RuntimeHandle.RuntimeName,
		AgentSessionID:  o.AgentSessionID,
		Prompt:          o.Prompt,
	}
}

// mergeMetadata overlays set fields of in onto base without clobbering an
// existing value with an empty one (a partial spawn write keeps the branch set
// at creation).
func mergeMetadata(base, in domain.SessionMetadata) domain.SessionMetadata {
	set := func(dst *string, v string) {
		if v != "" {
			*dst = v
		}
	}
	set(&base.Branch, in.Branch)
	set(&base.WorkspacePath, in.WorkspacePath)
	set(&base.RuntimeHandleID, in.RuntimeHandleID)
	set(&base.RuntimeName, in.RuntimeName)
	set(&base.AgentSessionID, in.AgentSessionID)
	set(&base.Prompt, in.Prompt)
	return base
}
