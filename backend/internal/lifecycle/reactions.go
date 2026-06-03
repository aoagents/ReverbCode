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

func hasUnresolvedComments(comments []ports.PRCommentObservation) bool {
	for _, c := range comments {
		if !c.Resolved {
			return true
		}
	}
	return false
}

func reviewContent(comments []ports.PRCommentObservation) (string, string) {
	var bodies []string
	var ids []string
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
