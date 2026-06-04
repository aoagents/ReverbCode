package lifecycle

import (
	"context"
	"strings"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const reviewMaxNudge = 3

type reactionState struct {
	mu       sync.Mutex
	seen     map[string]string
	attempts map[string]int
}

func newReactionState() reactionState {
	return reactionState{seen: map[string]string{}, attempts: map[string]int{}}
}

// ApplyPRObservation reacts to a fetched PR observation after the PR service has
// persisted it. It does not write PR rows; it owns PR-driven lifecycle effects
// and sends actionable agent nudges such as rebase, fix-CI, and
// address-review-feedback prompts.
func (m *Manager) ApplyPRObservation(ctx context.Context, id domain.SessionID, o ports.PRObservation) error {
	if !o.Fetched {
		return nil
	}
	if o.Merged {
		return m.MarkTerminated(ctx, id)
	}
	if o.Closed {
		return nil
	}
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil || !ok {
		return err
	}
	if rec.IsTerminated || rec.Activity.State == domain.ActivityWaitingInput {
		return nil
	}
	if o.CI == domain.CIFailing {
		for _, ch := range o.Checks {
			if ch.Status == domain.PRCheckFailed {
				msg := "CI is failing on your PR. Review the output below and push a fix."
				if ch.LogTail != "" {
					msg += "\n\nFailing output:\n" + ch.LogTail
				}
				return m.sendOnce(ctx, id, "ci:"+o.URL+":"+ch.Name, ch.CommitHash+":"+ch.LogTail, msg, 0)
			}
		}
	}
	if o.Review == domain.ReviewChangesRequest || hasUnresolvedComments(o.Comments) {
		comments, sig := reviewContent(o.Comments)
		msg := "A reviewer left feedback on your PR. Address it and push."
		if comments != "" {
			msg += "\n\n" + comments
		}
		if sig == "" {
			sig = string(o.Review)
		}
		return m.sendOnce(ctx, id, "review:"+o.URL, sig, msg, reviewMaxNudge)
	}
	if o.Mergeability == domain.MergeConflicting {
		return m.sendOnce(ctx, id, "merge-conflict:"+o.URL, string(o.Mergeability), "Your PR has merge conflicts. Rebase onto the base branch and resolve them.", 0)
	}
	return nil
}

// ApplySCMObservation is the provider-neutral lifecycle entrypoint used by the
// SCM observer. The existing reaction logic still operates on PRObservation, so
// lifecycle performs the compatibility projection internally instead of leaking
// the old PR DTO back into the observer/provider boundary.
func (m *Manager) ApplySCMObservation(ctx context.Context, id domain.SessionID, o ports.SCMObservation) error {
	if !o.Fetched {
		return nil
	}
	return m.ApplyPRObservation(ctx, id, scmToPRObservation(o))
}

func scmToPRObservation(o ports.SCMObservation) ports.PRObservation {
	pr := ports.PRObservation{
		Fetched:      o.Fetched,
		URL:          firstSCMNonEmpty(o.PR.URL, o.PR.HTMLURL),
		Number:       o.PR.Number,
		Draft:        o.PR.Draft,
		Merged:       o.PR.Merged,
		Closed:       o.PR.Closed,
		CI:           domain.CIState(o.CI.Summary),
		Review:       domain.ReviewDecision(o.Review.Decision),
		Mergeability: domain.Mergeability(o.Mergeability.State),
	}
	if pr.CI == "" {
		pr.CI = domain.CIUnknown
	}
	if pr.Review == "" {
		pr.Review = domain.ReviewNone
	}
	if pr.Mergeability == "" {
		pr.Mergeability = domain.MergeUnknown
	}
	checkCommit := firstSCMNonEmpty(o.CI.HeadSHA, o.PR.HeadSHA)
	for _, ch := range o.CI.FailedChecks {
		status := domain.PRCheckStatus(ch.Status)
		if status == "" {
			status = domain.PRCheckFailed
		}
		logTail := ch.LogTail
		if logTail == "" {
			logTail = o.CI.FailureLogTail
		}
		pr.Checks = append(pr.Checks, ports.PRCheckObservation{
			Name:       ch.Name,
			CommitHash: checkCommit,
			Status:     status,
			URL:        ch.URL,
			LogTail:    logTail,
		})
	}
	for _, th := range o.Review.Threads {
		if th.Resolved || th.IsBot {
			continue
		}
		for _, c := range th.Comments {
			if c.IsBot {
				continue
			}
			pr.Comments = append(pr.Comments, ports.PRCommentObservation{
				ID:       c.ID,
				Author:   c.Author,
				File:     th.Path,
				Line:     th.Line,
				Body:     c.Body,
				Resolved: th.Resolved,
			})
		}
	}
	return pr
}

func firstSCMNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func hasUnresolvedComments(comments []ports.PRCommentObservation) bool {
	for _, c := range comments {
		if !c.Resolved {
			return true
		}
	}
	return false
}

func reviewContent(comments []ports.PRCommentObservation) (string, string) {
	bodies := make([]string, 0, len(comments))
	ids := make([]string, 0, len(comments))
	for _, c := range comments {
		if c.Resolved {
			continue
		}
		bodies = append(bodies, c.Body)
		ids = append(ids, c.ID)
	}
	return strings.Join(bodies, "\n\n"), strings.Join(ids, ",")
}

func (m *Manager) sendOnce(ctx context.Context, id domain.SessionID, key, sig, msg string, maxAttempts int) error {
	if m.messenger == nil {
		return nil
	}
	m.react.mu.Lock()
	if m.react.seen[key] == sig {
		m.react.mu.Unlock()
		return nil
	}
	attempts := m.react.attempts[key]
	if maxAttempts > 0 && attempts >= maxAttempts {
		m.react.mu.Unlock()
		return nil
	}
	if err := m.messenger.Send(ctx, id, msg); err != nil {
		m.react.mu.Unlock()
		return err
	}
	m.react.seen[key] = sig
	m.react.attempts[key] = attempts + 1
	m.react.mu.Unlock()
	return nil
}
