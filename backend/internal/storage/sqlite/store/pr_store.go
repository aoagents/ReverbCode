package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// The pr / pr_checks / pr_comment rows are modelled by domain.PullRequest /
// domain.PullRequestCheck / domain.PullRequestComment — flat tables, one shared type per table.
// This layer only maps those to/from the sqlc gen.* params: the bool PR flags
// become the single pr.pr_state column, empty enums default to their
// "nothing known yet" value (matching the CHECK constraints), and ints widen to
// int64.

// Compile-time proof that *Store satisfies both ports it is wired into, so a
// drift between either interface and this implementation fails here at the point
// of definition rather than later at the call sites in lifecycle_wiring / tests.
var (
	_ ports.PRWriter = (*Store)(nil)
)

// WritePR persists a full PR observation — scalar facts, check runs, and the
// replacement comment set — in one write transaction, so the rows and the
// change_log events their triggers emit are committed all-or-nothing. The scalar
// PR upsert runs first so the checks'/comments' CDC triggers can resolve the
// session id from the pr row within the same transaction.
func (s *Store) WritePR(ctx context.Context, pr domain.PullRequest, checks []domain.PullRequestCheck, comments []domain.PullRequestComment) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "write pr observation", func(q *gen.Queries) error {
		existing, err := q.GetPR(ctx, pr.URL)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil && existing.SessionID != pr.SessionID {
			return fmt.Errorf("pr %s already belongs to session %s", pr.URL, existing.SessionID)
		}
		if err := q.UpsertPR(ctx, genPRParams(pr)); err != nil {
			return err
		}
		for _, c := range checks {
			if err := q.UpsertPRCheck(ctx, genCheckParams(pr.URL, c)); err != nil {
				return err
			}
		}
		if err := q.DeletePRComments(ctx, pr.URL); err != nil {
			return err
		}
		for _, c := range comments {
			if err := q.InsertPRComment(ctx, genCommentParams(pr.URL, c)); err != nil {
				return fmt.Errorf("comment %q: %w", c.ID, err)
			}
		}
		return nil
	})
}

// GetPR returns the PR facts for a URL, or ok=false if absent.
func (s *Store) GetPR(ctx context.Context, url string) (domain.PullRequest, bool, error) {
	p, err := s.qr.GetPR(ctx, url)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.PullRequest{}, false, nil
	}
	if err != nil {
		return domain.PullRequest{}, false, fmt.Errorf("get pr %s: %w", url, err)
	}
	return prRowFromGen(p), true, nil
}

// ListPRsBySession returns every PR owned by a session, newest first.
func (s *Store) ListPRsBySession(ctx context.Context, sessionID domain.SessionID) ([]domain.PullRequest, error) {
	rows, err := s.qr.ListPRsBySession(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list prs for %s: %w", sessionID, err)
	}
	out := make([]domain.PullRequest, 0, len(rows))
	for _, p := range rows {
		out = append(out, prRowFromGen(p))
	}
	return out, nil
}

// ListChecks returns every recorded check run for a PR.
func (s *Store) ListChecks(ctx context.Context, prURL string) ([]domain.PullRequestCheck, error) {
	rows, err := s.qr.ListChecksByPR(ctx, prURL)
	if err != nil {
		return nil, fmt.Errorf("list checks %s: %w", prURL, err)
	}
	out := make([]domain.PullRequestCheck, 0, len(rows))
	for _, c := range rows {
		out = append(out, checkRowFromGen(c))
	}
	return out, nil
}

// ListPRComments returns a PR's review comments, oldest first.
func (s *Store) ListPRComments(ctx context.Context, prURL string) ([]domain.PullRequestComment, error) {
	rows, err := s.qr.ListPRComments(ctx, prURL)
	if err != nil {
		return nil, fmt.Errorf("list pr comments %s: %w", prURL, err)
	}
	out := make([]domain.PullRequestComment, 0, len(rows))
	for _, c := range rows {
		out = append(out, commentFromGen(c))
	}
	return out, nil
}

// ---- domain <-> gen mapping ----

// prState collapses the PR's bools into the single pr.state column value.
func prState(r domain.PullRequest) domain.PRState {
	switch {
	case r.Merged:
		return domain.PRStateMerged
	case r.Closed:
		return domain.PRStateClosed
	case r.Draft:
		return domain.PRStateDraft
	default:
		return domain.PRStateOpen
	}
}

func genPRParams(r domain.PullRequest) gen.UpsertPRParams {
	return gen.UpsertPRParams{
		URL:            r.URL,
		SessionID:      r.SessionID,
		Number:         int64(r.Number),
		PRState:        prState(r),
		ReviewDecision: reviewOrDefault(r.Review),
		CIState:        ciOrDefault(r.CI),
		Mergeability:   mergeabilityOrDefault(r.Mergeability),
		UpdatedAt:      r.UpdatedAt,
	}
}

func reviewOrDefault(v domain.ReviewDecision) domain.ReviewDecision {
	if v == "" {
		return domain.ReviewNone
	}
	return v
}

func ciOrDefault(v domain.CIState) domain.CIState {
	if v == "" {
		return domain.CIUnknown
	}
	return v
}

func mergeabilityOrDefault(v domain.Mergeability) domain.Mergeability {
	if v == "" {
		return domain.MergeUnknown
	}
	return v
}

func prRowFromGen(p gen.PR) domain.PullRequest {
	return domain.PullRequest{
		URL:          p.URL,
		SessionID:    p.SessionID,
		Number:       int(p.Number),
		Draft:        p.PRState == domain.PRStateDraft,
		Merged:       p.PRState == domain.PRStateMerged,
		Closed:       p.PRState == domain.PRStateClosed,
		CI:           p.CIState,
		Review:       p.ReviewDecision,
		Mergeability: p.Mergeability,
		UpdatedAt:    p.UpdatedAt,
	}
}

func genCheckParams(prURL string, c domain.PullRequestCheck) gen.UpsertPRCheckParams {
	status := c.Status
	if status == "" {
		status = domain.PRCheckUnknown
	}
	return gen.UpsertPRCheckParams{
		PRURL: prURL, Name: c.Name, CommitHash: c.CommitHash,
		Status: status, URL: c.URL, LogTail: c.LogTail, CreatedAt: c.CreatedAt,
	}
}

func checkRowFromGen(c gen.PRCheck) domain.PullRequestCheck {
	return domain.PullRequestCheck{
		Name: c.Name, CommitHash: c.CommitHash, Status: c.Status,
		URL: c.URL, LogTail: c.LogTail, CreatedAt: c.CreatedAt,
	}
}

func genCommentParams(prURL string, c domain.PullRequestComment) gen.InsertPRCommentParams {
	return gen.InsertPRCommentParams{
		PRURL: prURL, CommentID: c.ID, Author: c.Author, File: c.File,
		Line: int64(c.Line), Body: c.Body, Resolved: c.Resolved, CreatedAt: c.CreatedAt,
	}
}

func commentFromGen(c gen.PRComment) domain.PullRequestComment {
	return domain.PullRequestComment{
		ID: c.CommentID, Author: c.Author, File: c.File, Line: int(c.Line),
		Body: c.Body, Resolved: c.Resolved, CreatedAt: c.CreatedAt,
	}
}
