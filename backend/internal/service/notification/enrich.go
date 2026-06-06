package notification

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// EnrichedFacts are local durable facts used by actions, copy, data, and
// fingerprinting. They intentionally contain no network-fetched data.
type EnrichedFacts struct {
	Session domain.SessionRecord
	Project domain.ProjectRecord

	PR            *domain.PullRequest
	PRURL         string
	Checks        []domain.PullRequestCheck
	FailedChecks  []domain.PullRequestCheck
	Comments      []domain.PullRequestComment
	ReviewThreads []domain.PullRequestReviewThread

	SessionLabel string
	ProjectLabel string
}

func (s *Service) enrich(ctx context.Context, intent domain.NotificationIntent) (EnrichedFacts, error) {
	session, ok, err := s.store.GetSession(ctx, intent.SessionID)
	if err != nil {
		return EnrichedFacts{}, fmt.Errorf("notification: get session %s: %w", intent.SessionID, err)
	}
	if !ok {
		return EnrichedFacts{}, fmt.Errorf("notification: unknown session %s", intent.SessionID)
	}
	if session.ProjectID != intent.ProjectID {
		return EnrichedFacts{}, fmt.Errorf("notification: session %s belongs to project %s, not %s", intent.SessionID, session.ProjectID, intent.ProjectID)
	}
	project, ok, err := s.store.GetProject(ctx, string(intent.ProjectID))
	if err != nil {
		return EnrichedFacts{}, fmt.Errorf("notification: get project %s: %w", intent.ProjectID, err)
	}
	if !ok {
		return EnrichedFacts{}, fmt.Errorf("notification: unknown project %s", intent.ProjectID)
	}

	facts := EnrichedFacts{
		Session:      session,
		Project:      project,
		SessionLabel: sessionLabel(session),
		ProjectLabel: projectLabel(project),
	}
	facts.PRURL = strings.TrimSpace(intent.Context.PRURL)

	prs, err := s.store.ListPRsBySession(ctx, intent.SessionID)
	if err != nil {
		return EnrichedFacts{}, fmt.Errorf("notification: list PRs for %s: %w", intent.SessionID, err)
	}
	facts.PR = choosePR(prs, facts.PRURL)
	if facts.PR != nil {
		facts.PRURL = facts.PR.URL
	} else if facts.PRURL != "" {
		facts.PR = &domain.PullRequest{URL: facts.PRURL, HTMLURL: facts.PRURL, SessionID: intent.SessionID}
	}

	if facts.PRURL != "" {
		checks, err := s.store.ListChecks(ctx, facts.PRURL)
		if err != nil {
			return EnrichedFacts{}, fmt.Errorf("notification: list checks for %s: %w", facts.PRURL, err)
		}
		facts.Checks = checks
		facts.FailedChecks = failedChecks(checks, intent)
		if len(facts.FailedChecks) == 0 && intent.Context.CheckName != "" {
			facts.FailedChecks = []domain.PullRequestCheck{{
				Name:       intent.Context.CheckName,
				CommitHash: intent.Context.CommitHash,
				Status:     domain.PRCheckFailed,
				URL:        intent.Context.CheckURL,
				CreatedAt:  intent.OccurredAt,
			}}
		}
		comments, err := s.store.ListPRComments(ctx, facts.PRURL)
		if err != nil {
			return EnrichedFacts{}, fmt.Errorf("notification: list PR comments for %s: %w", facts.PRURL, err)
		}
		facts.Comments = comments
		threads, err := s.store.ListPRReviewThreads(ctx, facts.PRURL)
		if err != nil {
			return EnrichedFacts{}, fmt.Errorf("notification: list PR review threads for %s: %w", facts.PRURL, err)
		}
		facts.ReviewThreads = threads
	}
	return facts, nil
}

func choosePR(prs []domain.PullRequest, wantURL string) *domain.PullRequest {
	if len(prs) == 0 {
		return nil
	}
	if wantURL != "" {
		for i := range prs {
			if prs[i].URL == wantURL || prs[i].HTMLURL == wantURL {
				pr := prs[i]
				return &pr
			}
		}
	}
	pr := prs[0]
	return &pr
}

func failedChecks(checks []domain.PullRequestCheck, intent domain.NotificationIntent) []domain.PullRequestCheck {
	out := make([]domain.PullRequestCheck, 0, len(checks))
	for _, c := range checks {
		if c.Status != domain.PRCheckFailed && c.Status != domain.PRCheckCancelled {
			continue
		}
		if intent.Context.CheckName != "" && c.Name != intent.Context.CheckName {
			continue
		}
		if intent.Context.CommitHash != "" && c.CommitHash != intent.Context.CommitHash {
			continue
		}
		out = append(out, c)
	}
	return out
}

func sessionLabel(s domain.SessionRecord) string {
	if strings.TrimSpace(s.DisplayName) != "" {
		return strings.TrimSpace(s.DisplayName)
	}
	if s.IssueID != "" {
		return fmt.Sprintf("%s (%s)", s.ID, s.IssueID)
	}
	return string(s.ID)
}

func projectLabel(p domain.ProjectRecord) string {
	if strings.TrimSpace(p.DisplayName) != "" {
		return strings.TrimSpace(p.DisplayName)
	}
	if p.ID != "" {
		return p.ID
	}
	return p.Path
}

func subjectForFacts(f EnrichedFacts) domain.NotificationSubject {
	s := domain.NotificationSubject{
		Kind:        "session",
		Label:       f.SessionLabel,
		ProjectID:   f.Session.ProjectID,
		SessionID:   f.Session.ID,
		ProjectName: f.ProjectLabel,
	}
	if f.PR != nil {
		s.Kind = "pull_request"
		s.PRURL = f.PR.URL
		s.PRNumber = f.PR.Number
		s.PRTitle = f.PR.Title
		if f.PR.Title != "" {
			s.Label = f.PR.Title
		}
	} else if f.PRURL != "" {
		s.Kind = "pull_request"
		s.PRURL = f.PRURL
	}
	return s
}

func dataForIntent(intent domain.NotificationIntent, f EnrichedFacts) map[string]any {
	data := map[string]any{
		"intent": map[string]any{
			"type":       intent.Type,
			"priority":   intent.Priority,
			"source":     intent.Source,
			"dedupeKey":  intent.DedupeKey,
			"occurredAt": intent.OccurredAt.UTC().Format(time.RFC3339Nano),
			"context":    intent.Context,
		},
		"project": map[string]any{
			"id":            f.Project.ID,
			"displayName":   f.Project.DisplayName,
			"path":          f.Project.Path,
			"repoOriginUrl": f.Project.RepoOriginURL,
		},
		"session": map[string]any{
			"id":             f.Session.ID,
			"displayName":    f.Session.DisplayName,
			"label":          f.SessionLabel,
			"kind":           f.Session.Kind,
			"issueId":        f.Session.IssueID,
			"activityState":  f.Session.Activity.State,
			"activityLastAt": f.Session.Activity.LastActivityAt.UTC().Format(time.RFC3339Nano),
			"isTerminated":   f.Session.IsTerminated,
		},
	}
	if f.PR != nil || f.PRURL != "" {
		pr := f.PR
		if pr == nil {
			pr = &domain.PullRequest{URL: f.PRURL}
		}
		data["pr"] = map[string]any{
			"url":            pr.URL,
			"htmlUrl":        firstNonEmpty(pr.HTMLURL, pr.URL),
			"number":         pr.Number,
			"title":          pr.Title,
			"headSha":        pr.HeadSHA,
			"baseSha":        pr.BaseSHA,
			"mergeCommitSha": pr.MergeCommitSHA,
			"ci":             pr.CI,
			"review":         pr.Review,
			"mergeability":   pr.Mergeability,
		}
	}
	if len(f.Checks) > 0 || intent.Context.CheckName != "" {
		data["ci"] = map[string]any{
			"checkName":    intent.Context.CheckName,
			"checkUrl":     intent.Context.CheckURL,
			"commitHash":   intent.Context.CommitHash,
			"failedCount":  len(f.FailedChecks),
			"failedChecks": checkData(f.FailedChecks),
		}
	}
	if len(f.Comments) > 0 || len(f.ReviewThreads) > 0 || len(intent.Context.ReviewIDs) > 0 || len(intent.Context.ThreadIDs) > 0 {
		data["review"] = map[string]any{
			"reviewIds":              sortedCopy(intent.Context.ReviewIDs),
			"threadIds":              sortedCopy(intent.Context.ThreadIDs),
			"commentCount":           len(f.Comments),
			"unresolvedCommentCount": unresolvedCommentCount(f.Comments),
			"threadCount":            len(f.ReviewThreads),
			"unresolvedThreadCount":  unresolvedThreadCount(f.ReviewThreads),
		}
	}
	if intent.Context.MergeState != "" {
		data["merge"] = map[string]any{"state": intent.Context.MergeState}
	}
	return data
}

func checkData(checks []domain.PullRequestCheck) []map[string]any {
	out := make([]map[string]any, 0, len(checks))
	for _, c := range checks {
		out = append(out, map[string]any{
			"name":       c.Name,
			"commitHash": c.CommitHash,
			"status":     c.Status,
			"url":        c.URL,
			"details":    c.Details,
			"logTail":    bounded(c.LogTail, 4000),
		})
	}
	return out
}

func unresolvedCommentCount(comments []domain.PullRequestComment) int {
	var n int
	for _, c := range comments {
		if !c.Resolved && !c.IsBot {
			n++
		}
	}
	return n
}

func unresolvedThreadCount(threads []domain.PullRequestReviewThread) int {
	var n int
	for _, th := range threads {
		if !th.Resolved && !th.IsBot {
			n++
		}
	}
	return n
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func bounded(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit]
}
