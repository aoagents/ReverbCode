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

// The pr / pr_checks / pr_comment rows are modelled by domain.PRRow /
// domain.PRCheckRow / domain.PRComment — flat tables, one shared type per table.
// This layer only maps those to/from the sqlc gen.* params: the bool PR state
// becomes the single pr.state column, empty enums default to their
// "nothing known yet" value (matching the CHECK constraints), and ints widen to
// int64.

// Compile-time proof that *Store satisfies both ports it is wired into, so a
// drift between either interface and this implementation fails here at the point
// of definition rather than later at the call sites in lifecycle_wiring / tests.
var (
	_ ports.SessionStore = (*Store)(nil)
	_ ports.PRWriter     = (*Store)(nil)
)

// UpsertPR inserts or replaces the scalar PR facts for a PR URL.
func (s *Store) UpsertPR(ctx context.Context, r domain.PRRow) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.qw.UpsertPR(ctx, genPRParams(r))
}

// WritePR persists a full PR observation — scalar facts, check runs, and the
// replacement comment set — in one write transaction, so the rows and the
// change_log events their triggers emit are committed all-or-nothing. The scalar
// PR upsert runs first so the checks'/comments' CDC triggers can resolve the
// session id from the pr row within the same transaction.
func (s *Store) WritePR(ctx context.Context, pr domain.PRRow, checks []domain.PRCheckRow, comments []domain.PRComment) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "write pr observation", func(q *gen.Queries) error {
		if err := q.UpsertPR(ctx, genPRParams(pr)); err != nil {
			return err
		}
		for _, c := range checks {
			if err := q.UpsertPRCheck(ctx, genCheckParams(c)); err != nil {
				return err
			}
		}
		if err := q.DeletePRComments(ctx, pr.URL); err != nil {
			return err
		}
		for _, c := range comments {
			if err := q.UpsertPRComment(ctx, genCommentParams(pr.URL, c)); err != nil {
				return fmt.Errorf("comment %q: %w", c.ID, err)
			}
		}
		return nil
	})
}

// GetPR returns the PR facts for a URL, or ok=false if absent.
func (s *Store) GetPR(ctx context.Context, url string) (domain.PRRow, bool, error) {
	p, err := s.qr.GetPR(ctx, url)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.PRRow{}, false, nil
	}
	if err != nil {
		return domain.PRRow{}, false, fmt.Errorf("get pr %s: %w", url, err)
	}
	return prRowFromGen(p), true, nil
}

// ListPRsBySession returns every PR owned by a session, newest first.
func (s *Store) ListPRsBySession(ctx context.Context, sessionID string) ([]domain.PRRow, error) {
	rows, err := s.qr.ListPRsBySession(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list prs for %s: %w", sessionID, err)
	}
	out := make([]domain.PRRow, 0, len(rows))
	for _, p := range rows {
		out = append(out, prRowFromGen(p))
	}
	return out, nil
}

// DeletePR removes a PR (cascades to its checks + comments).
func (s *Store) DeletePR(ctx context.Context, url string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.qw.DeletePR(ctx, url)
}

// RecordCheck upserts a CI check run. Re-polling the same (pr, name, commit)
// updates the same row; a new commit creates a new row (a fresh agent attempt).
func (s *Store) RecordCheck(ctx context.Context, r domain.PRCheckRow) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.qw.UpsertPRCheck(ctx, genCheckParams(r))
}

// RecentCheckStatuses returns the statuses of the last `limit` runs of a check,
// most-recent first. The CI-fix-loop brake reads this: "last 3 all failed?".
func (s *Store) RecentCheckStatuses(ctx context.Context, prURL, name string, limit int) ([]string, error) {
	rows, err := s.qr.ListRecentChecks(ctx, gen.ListRecentChecksParams{
		PrUrl: prURL, Name: name, Limit: int64(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("recent checks %s/%s: %w", prURL, name, err)
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Status)
	}
	return out, nil
}

// ListChecks returns every recorded check run for a PR.
func (s *Store) ListChecks(ctx context.Context, prURL string) ([]domain.PRCheckRow, error) {
	rows, err := s.qr.ListChecksByPR(ctx, prURL)
	if err != nil {
		return nil, fmt.Errorf("list checks %s: %w", prURL, err)
	}
	out := make([]domain.PRCheckRow, 0, len(rows))
	for _, c := range rows {
		out = append(out, checkRowFromGen(c))
	}
	return out, nil
}

// ReplacePRComments atomically replaces the full comment set for a PR (each SCM
// fetch reports the current set, so a replace keeps it in sync).
func (s *Store) ReplacePRComments(ctx context.Context, prURL string, comments []domain.PRComment) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "replace pr comments", func(q *gen.Queries) error {
		if err := q.DeletePRComments(ctx, prURL); err != nil {
			return err
		}
		for _, c := range comments {
			if err := q.UpsertPRComment(ctx, genCommentParams(prURL, c)); err != nil {
				return fmt.Errorf("comment %q: %w", c.ID, err)
			}
		}
		return nil
	})
}

// ListPRComments returns a PR's review comments, oldest first.
func (s *Store) ListPRComments(ctx context.Context, prURL string) ([]domain.PRComment, error) {
	rows, err := s.qr.ListPRComments(ctx, prURL)
	if err != nil {
		return nil, fmt.Errorf("list pr comments %s: %w", prURL, err)
	}
	out := make([]domain.PRComment, 0, len(rows))
	for _, c := range rows {
		out = append(out, commentFromGen(c))
	}
	return out, nil
}

// ---- domain <-> gen mapping ----

// prState collapses the PR's bools into the single pr.state column value.
func prState(r domain.PRRow) string {
	switch {
	case r.Merged:
		return "merged"
	case r.Closed:
		return "closed"
	case r.Draft:
		return "draft"
	default:
		return "open"
	}
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func genPRParams(r domain.PRRow) gen.UpsertPRParams {
	return gen.UpsertPRParams{
		Url:            r.URL,
		SessionID:      r.SessionID,
		Number:         int64(r.Number),
		PrState:        prState(r),
		ReviewDecision: orDefault(string(r.Review), "none"),
		CiState:        orDefault(string(r.CI), "unknown"),
		Mergeability:   orDefault(string(r.Mergeability), "unknown"),
		UpdatedAt:      r.UpdatedAt,
	}
}

func prRowFromGen(p gen.Pr) domain.PRRow {
	return domain.PRRow{
		URL:          p.Url,
		SessionID:    p.SessionID,
		Number:       int(p.Number),
		Draft:        p.PrState == "draft",
		Merged:       p.PrState == "merged",
		Closed:       p.PrState == "closed",
		CI:           domain.CIState(p.CiState),
		Review:       domain.ReviewDecision(p.ReviewDecision),
		Mergeability: domain.Mergeability(p.Mergeability),
		UpdatedAt:    p.UpdatedAt,
	}
}

func genCheckParams(c domain.PRCheckRow) gen.UpsertPRCheckParams {
	status := c.Status
	if status == "" {
		status = "unknown"
	}
	return gen.UpsertPRCheckParams{
		PrUrl: c.PRURL, Name: c.Name, CommitHash: c.CommitHash,
		Status: status, Url: c.URL, LogTail: c.LogTail, CreatedAt: c.CreatedAt,
	}
}

func checkRowFromGen(c gen.PrCheck) domain.PRCheckRow {
	return domain.PRCheckRow{
		PRURL: c.PrUrl, Name: c.Name, CommitHash: c.CommitHash,
		Status: c.Status, URL: c.Url, LogTail: c.LogTail, CreatedAt: c.CreatedAt,
	}
}

func genCommentParams(prURL string, c domain.PRComment) gen.UpsertPRCommentParams {
	return gen.UpsertPRCommentParams{
		PrUrl: prURL, CommentID: c.ID, Author: c.Author, File: c.File,
		Line: int64(c.Line), Body: c.Body, Resolved: boolToInt(c.Resolved), CreatedAt: c.CreatedAt,
	}
}

func commentFromGen(c gen.PrComment) domain.PRComment {
	return domain.PRComment{
		ID: c.CommentID, Author: c.Author, File: c.File, Line: int(c.Line),
		Body: c.Body, Resolved: c.Resolved != 0, CreatedAt: c.CreatedAt,
	}
}
