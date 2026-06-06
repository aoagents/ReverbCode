package notification

import (
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func buildActions(intent domain.NotificationIntent, facts EnrichedFacts) []domain.NotificationAction {
	openSession := domain.NotificationAction{
		ID:      "open_session",
		Label:   "Open session",
		Kind:    "route",
		Route:   "session",
		Payload: map[string]any{"projectId": intent.ProjectID, "sessionId": intent.SessionID},
	}
	viewPR := func(primary bool) (domain.NotificationAction, bool) {
		url := prURL(facts, intent)
		if url == "" {
			return domain.NotificationAction{}, false
		}
		return domain.NotificationAction{ID: "view_pr", Label: "View PR", Kind: "link", URL: url, Primary: primary}, true
	}
	viewCI := func() domain.NotificationAction {
		url := intent.Context.CheckURL
		if url == "" && len(facts.FailedChecks) > 0 {
			url = facts.FailedChecks[0].URL
		}
		if url == "" {
			return domain.NotificationAction{ID: "view_ci", Label: "View CI", Kind: "callback", Payload: map[string]any{"checkName": intent.Context.CheckName, "prUrl": prURL(facts, intent)}}
		}
		return domain.NotificationAction{ID: "view_ci", Label: "View CI", Kind: "link", URL: url}
	}
	viewReview := func() domain.NotificationAction {
		for _, c := range facts.Comments {
			if c.URL != "" {
				return domain.NotificationAction{ID: "view_review", Label: "View review", Kind: "link", URL: c.URL}
			}
		}
		url := prURL(facts, intent)
		if url == "" {
			return domain.NotificationAction{ID: "view_review", Label: "View review", Kind: "callback", Payload: map[string]any{"reviewIds": intent.Context.ReviewIDs, "threadIds": intent.Context.ThreadIDs}}
		}
		return domain.NotificationAction{ID: "view_review", Label: "View review", Kind: "link", URL: url}
	}

	var actions []domain.NotificationAction
	add := func(a domain.NotificationAction, ok bool) {
		if ok {
			actions = append(actions, a)
		}
	}
	switch intent.Type {
	case domain.NotificationCIFailing:
		openSession.Primary = true
		actions = append(actions, openSession, viewCI())
		add(viewPR(false))
	case domain.NotificationReviewChanges:
		openSession.Primary = true
		actions = append(actions, openSession, viewReview())
		add(viewPR(false))
	case domain.NotificationMergeConflicts:
		openSession.Primary = true
		actions = append(actions, openSession)
		add(viewPR(false))
	case domain.NotificationMergeReady, domain.NotificationMergeCompleted:
		add(viewPR(true))
		openSession.Primary = len(actions) == 0
		actions = append(actions, openSession)
	case domain.NotificationSessionInput, domain.NotificationSessionExited:
		openSession.Primary = true
		actions = append(actions, openSession)
	}
	return actions
}

func prURL(facts EnrichedFacts, intent domain.NotificationIntent) string {
	if facts.PR != nil {
		if facts.PR.HTMLURL != "" {
			return facts.PR.HTMLURL
		}
		if facts.PR.URL != "" {
			return facts.PR.URL
		}
	}
	if facts.PRURL != "" {
		return facts.PRURL
	}
	return intent.Context.PRURL
}
