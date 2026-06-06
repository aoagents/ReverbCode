package notification

import (
	"context"
	"fmt"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Maker turns intent plus enriched facts and actions into channel-neutral copy.
type Maker interface {
	Make(ctx context.Context, input MakeInput) (domain.NotificationContent, error)
}

// MakeInput is the central maker input.
type MakeInput struct {
	Intent  domain.NotificationIntent
	Facts   EnrichedFacts
	Actions []domain.NotificationAction
}

// DefaultMaker produces concise, canonical fallback copy for all V1
// notification types. It is intentionally not Slack/email/desktop/dashboard
// formatting.
type DefaultMaker struct{}

// Make returns concise channel-neutral fallback copy for a notification.
func (DefaultMaker) Make(_ context.Context, input MakeInput) (domain.NotificationContent, error) {
	session := input.Facts.SessionLabel
	if session == "" {
		session = string(input.Intent.SessionID)
	}
	var title, summary string
	switch input.Intent.Type {
	case domain.NotificationCIFailing:
		n := len(input.Facts.FailedChecks)
		if n == 0 && input.Intent.Context.CheckName != "" {
			n = 1
		}
		title = "CI failed"
		if n == 1 {
			summary = fmt.Sprintf("%s has 1 failing check.", session)
		} else if n > 1 {
			summary = fmt.Sprintf("%s has %d failing checks.", session, n)
		} else {
			summary = fmt.Sprintf("%s has failing CI.", session)
		}
	case domain.NotificationReviewChanges:
		title = "Changes requested"
		summary = fmt.Sprintf("Review feedback is waiting on %s.", session)
	case domain.NotificationMergeConflicts:
		title = "Merge conflicts"
		summary = fmt.Sprintf("%s needs a rebase before it can merge.", session)
	case domain.NotificationMergeReady:
		title = "Ready to merge"
		summary = fmt.Sprintf("%s is approved and green.", session)
	case domain.NotificationMergeCompleted:
		title = "Merged"
		summary = fmt.Sprintf("%s was merged.", session)
	case domain.NotificationSessionInput:
		title = "Input needed"
		summary = fmt.Sprintf("%s is waiting for you.", session)
	case domain.NotificationSessionExited:
		title = "Session exited"
		summary = fmt.Sprintf("%s stopped unexpectedly.", session)
	default:
		return domain.NotificationContent{}, fmt.Errorf("unsupported notification type %q", input.Intent.Type)
	}
	return domain.NotificationContent{Title: truncateRunes(title, 40), Summary: truncateRunes(summary, 120), Body: truncateRunes(summary, 500)}, nil
}

func truncateRunes(s string, limit int) string {
	if limit <= 0 || utf8.RuneCountInString(s) <= limit {
		return s
	}
	runes := []rune(s)
	if limit <= 1 {
		return string(runes[:limit])
	}
	return string(runes[:limit-1]) + "…"
}
