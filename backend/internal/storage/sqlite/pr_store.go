package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// PRRow is the scalar PR facts row (the pr table), keyed by normalized URL. One
// session can own many PRs; a PR belongs to one session (session_id FK).
type PRRow struct {
	URL            string
	SessionID      string
	Number         int64
	State          string // draft | open | merged | closed
	ReviewDecision string // none | approved | changes_requested | review_required
	CIState        string // unknown | pending | passing | failing
	Mergeability   string // unknown | mergeable | conflicting | blocked | unstable
	UpdatedAt      time.Time
}

// UpsertPR inserts or replaces the scalar PR facts for a PR URL. Empty enum
// fields default to their "nothing known yet" value so a partial row is valid
// against the CHECK constraints (matches the domain zero values none/unknown).
func (s *Store) UpsertPR(ctx context.Context, r PRRow) error {
	r = r.withDefaults()
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.qw.UpsertPR(ctx, gen.UpsertPRParams{
		Url:            r.URL,
		SessionID:      r.SessionID,
		Number:         r.Number,
		PrState:        r.State,
		ReviewDecision: r.ReviewDecision,
		CiState:        r.CIState,
		Mergeability:   r.Mergeability,
		UpdatedAt:      r.UpdatedAt,
	})
}

// WritePRObservation persists a full PR observation — scalar facts, check runs,
// and the replacement comment set — in one write transaction, so the rows and
// the change_log events their triggers emit are committed all-or-nothing. The
// scalar PR upsert runs first so the checks'/comments' CDC triggers can resolve
// the session id from the pr row within the same transaction.
func (s *Store) WritePRObservation(ctx context.Context, pr PRRow, checks []PRCheckRow, comments []PRCommentRow) error {
	pr = pr.withDefaults()
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "write pr observation", func(q *gen.Queries) error {
		if err := q.UpsertPR(ctx, gen.UpsertPRParams{
			Url: pr.URL, SessionID: pr.SessionID, Number: pr.Number,
			PrState: pr.State, ReviewDecision: pr.ReviewDecision,
			CiState: pr.CIState, Mergeability: pr.Mergeability, UpdatedAt: pr.UpdatedAt,
		}); err != nil {
			return err
		}
		for _, c := range checks {
			if c.Status == "" {
				c.Status = "unknown"
			}
			if err := q.UpsertPRCheck(ctx, gen.UpsertPRCheckParams{
				PrUrl: c.PRURL, Name: c.Name, CommitHash: c.CommitHash,
				Status: c.Status, Url: c.URL, LogTail: c.LogTail, CreatedAt: c.CreatedAt,
			}); err != nil {
				return err
			}
		}
		if err := q.DeletePRComments(ctx, pr.URL); err != nil {
			return err
		}
		for _, cm := range comments {
			if err := q.UpsertPRComment(ctx, gen.UpsertPRCommentParams{
				PrUrl: pr.URL, CommentID: cm.CommentID, Author: cm.Author, File: cm.File,
				Line: cm.Line, Body: cm.Body, Resolved: boolToInt(cm.Resolved), CreatedAt: cm.CreatedAt,
			}); err != nil {
				return fmt.Errorf("comment %q: %w", cm.CommentID, err)
			}
		}
		return nil
	})
}

// withDefaults fills empty enum fields with their "nothing known yet" value so a
// partial row satisfies the CHECK constraints (matches UpsertPR).
func (r PRRow) withDefaults() PRRow {
	if r.State == "" {
		r.State = "open"
	}
	if r.ReviewDecision == "" {
		r.ReviewDecision = "none"
	}
	if r.CIState == "" {
		r.CIState = "unknown"
	}
	if r.Mergeability == "" {
		r.Mergeability = "unknown"
	}
	return r
}

// GetPR returns the PR facts for a URL, or ok=false if absent.
func (s *Store) GetPR(ctx context.Context, url string) (PRRow, bool, error) {
	p, err := s.qr.GetPR(ctx, url)
	if errors.Is(err, sql.ErrNoRows) {
		return PRRow{}, false, nil
	}
	if err != nil {
		return PRRow{}, false, fmt.Errorf("get pr %s: %w", url, err)
	}
	return prRowFromGen(p), true, nil
}

// ListPRsBySession returns every PR owned by a session, newest first.
func (s *Store) ListPRsBySession(ctx context.Context, sessionID string) ([]PRRow, error) {
	rows, err := s.qr.ListPRsBySession(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list prs for %s: %w", sessionID, err)
	}
	out := make([]PRRow, 0, len(rows))
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

func prRowFromGen(p gen.Pr) PRRow {
	return PRRow{
		URL:            p.Url,
		SessionID:      p.SessionID,
		Number:         p.Number,
		State:          p.PrState,
		ReviewDecision: p.ReviewDecision,
		CIState:        p.CiState,
		Mergeability:   p.Mergeability,
		UpdatedAt:      p.UpdatedAt,
	}
}

// ---- pr_checks: CI run history ----

// PRCheckRow is one CI check run for a PR (one row per check name per commit).
type PRCheckRow struct {
	PRURL      string
	Name       string
	CommitHash string
	Status     string // unknown | queued | in_progress | passed | failed | skipped | cancelled
	URL        string
	LogTail    string
	CreatedAt  time.Time
}

// RecordCheck upserts a CI check run. Re-polling the same (pr, name, commit)
// updates the same row; a new commit creates a new row (a fresh agent attempt).
func (s *Store) RecordCheck(ctx context.Context, r PRCheckRow) error {
	if r.Status == "" {
		r.Status = "unknown"
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.qw.UpsertPRCheck(ctx, gen.UpsertPRCheckParams{
		PrUrl:      r.PRURL,
		Name:       r.Name,
		CommitHash: r.CommitHash,
		Status:     r.Status,
		Url:        r.URL,
		LogTail:    r.LogTail,
		CreatedAt:  r.CreatedAt,
	})
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
func (s *Store) ListChecks(ctx context.Context, prURL string) ([]PRCheckRow, error) {
	rows, err := s.qr.ListChecksByPR(ctx, prURL)
	if err != nil {
		return nil, fmt.Errorf("list checks %s: %w", prURL, err)
	}
	out := make([]PRCheckRow, 0, len(rows))
	for _, c := range rows {
		out = append(out, PRCheckRow{
			PRURL: c.PrUrl, Name: c.Name, CommitHash: c.CommitHash,
			Status: c.Status, URL: c.Url, LogTail: c.LogTail, CreatedAt: c.CreatedAt,
		})
	}
	return out, nil
}

// ---- pr_comment ----

// PRCommentRow is one review comment on a PR.
type PRCommentRow struct {
	PRURL     string
	CommentID string
	Author    string
	File      string
	Line      int64
	Body      string
	Resolved  bool
	CreatedAt time.Time
}

// ReplacePRComments atomically replaces the full comment set for a PR (each SCM
// fetch reports the current set, so a replace keeps it in sync).
func (s *Store) ReplacePRComments(ctx context.Context, prURL string, comments []PRCommentRow) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "replace pr comments", func(q *gen.Queries) error {
		if err := q.DeletePRComments(ctx, prURL); err != nil {
			return err
		}
		for _, c := range comments {
			if err := q.UpsertPRComment(ctx, gen.UpsertPRCommentParams{
				PrUrl:     prURL,
				CommentID: c.CommentID,
				Author:    c.Author,
				File:      c.File,
				Line:      c.Line,
				Body:      c.Body,
				Resolved:  boolToInt(c.Resolved),
				CreatedAt: c.CreatedAt,
			}); err != nil {
				return fmt.Errorf("comment %q: %w", c.CommentID, err)
			}
		}
		return nil
	})
}

// ListPRComments returns a PR's review comments, oldest first.
func (s *Store) ListPRComments(ctx context.Context, prURL string) ([]PRCommentRow, error) {
	rows, err := s.qr.ListPRComments(ctx, prURL)
	if err != nil {
		return nil, fmt.Errorf("list pr comments %s: %w", prURL, err)
	}
	out := make([]PRCommentRow, 0, len(rows))
	for _, c := range rows {
		out = append(out, PRCommentRow{
			PRURL: c.PrUrl, CommentID: c.CommentID, Author: c.Author, File: c.File,
			Line: c.Line, Body: c.Body, Resolved: c.Resolved != 0, CreatedAt: c.CreatedAt,
		})
	}
	return out, nil
}
