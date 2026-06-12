package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// UpsertReview inserts the per-worker review row, or reuses the existing one
// (session_id is unique) by refreshing its harness/pr_url/updated_at.
func (s *Store) UpsertReview(ctx context.Context, r domain.Review) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.qw.UpsertReview(ctx, gen.UpsertReviewParams{
		ID:        r.ID,
		SessionID: r.SessionID,
		ProjectID: r.ProjectID,
		Harness:   r.Harness,
		PRURL:     r.PRURL,
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
	})
}

// GetReviewBySession returns the review row for a worker session, ok=false if none.
func (s *Store) GetReviewBySession(ctx context.Context, id domain.SessionID) (domain.Review, bool, error) {
	row, err := s.qr.GetReviewBySession(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Review{}, false, nil
	}
	if err != nil {
		return domain.Review{}, false, fmt.Errorf("get review by session %s: %w", id, err)
	}
	return reviewFromRow(row), true, nil
}

// InsertReviewRun records a new review pass.
func (s *Store) InsertReviewRun(ctx context.Context, r domain.ReviewRun) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.qw.InsertReviewRun(ctx, gen.InsertReviewRunParams{
		ID:        r.ID,
		ReviewID:  r.ReviewID,
		SessionID: r.SessionID,
		Harness:   r.Harness,
		PRURL:     r.PRURL,
		Status:    r.Status,
		Verdict:   r.Verdict,
		Iteration: int64(r.Iteration),
		Body:      r.Body,
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
	})
}

// UpdateReviewRunResult sets the status/verdict/body of a review pass.
func (s *Store) UpdateReviewRunResult(ctx context.Context, id string, status domain.ReviewRunStatus, verdict domain.ReviewVerdict, body string, updatedAt time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.qw.UpdateReviewRunResult(ctx, gen.UpdateReviewRunResultParams{
		Status:    status,
		Verdict:   verdict,
		Body:      body,
		UpdatedAt: updatedAt,
		ID:        id,
	})
}

// GetLatestReviewRunBySession returns the most recent review pass for a worker
// session, ok=false if none.
func (s *Store) GetLatestReviewRunBySession(ctx context.Context, id domain.SessionID) (domain.ReviewRun, bool, error) {
	row, err := s.qr.GetLatestReviewRunBySession(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ReviewRun{}, false, nil
	}
	if err != nil {
		return domain.ReviewRun{}, false, fmt.Errorf("get latest review run for session %s: %w", id, err)
	}
	return reviewRunFromRow(row), true, nil
}

// ListReviewRunsBySession returns all review passes for a worker session, newest first.
func (s *Store) ListReviewRunsBySession(ctx context.Context, id domain.SessionID) ([]domain.ReviewRun, error) {
	rows, err := s.qr.ListReviewRunsBySession(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("list review runs for session %s: %w", id, err)
	}
	out := make([]domain.ReviewRun, 0, len(rows))
	for _, row := range rows {
		out = append(out, reviewRunFromRow(row))
	}
	return out, nil
}

func reviewFromRow(r gen.Review) domain.Review {
	return domain.Review{
		ID:        r.ID,
		SessionID: r.SessionID,
		ProjectID: r.ProjectID,
		Harness:   r.Harness,
		PRURL:     r.PRURL,
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
	}
}

func reviewRunFromRow(r gen.ReviewRun) domain.ReviewRun {
	return domain.ReviewRun{
		ID:        r.ID,
		ReviewID:  r.ReviewID,
		SessionID: r.SessionID,
		Harness:   r.Harness,
		PRURL:     r.PRURL,
		Status:    r.Status,
		Verdict:   r.Verdict,
		Iteration: int(r.Iteration),
		Body:      r.Body,
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
	}
}
