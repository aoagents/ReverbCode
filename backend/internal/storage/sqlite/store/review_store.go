package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// UpsertReview inserts the per-PR review row, or reuses the existing one for
// the same worker session and PR by refreshing its harness/handle/updated_at.
func (s *Store) UpsertReview(ctx context.Context, r domain.Review) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.qw.UpsertReview(ctx, gen.UpsertReviewParams{
		ID:               r.ID,
		SessionID:        r.SessionID,
		ProjectID:        r.ProjectID,
		Harness:          r.Harness,
		PRURL:            r.PRURL,
		ReviewerHandleID: r.ReviewerHandleID,
		CreatedAt:        r.CreatedAt,
		UpdatedAt:        r.UpdatedAt,
	})
}

// GetReviewBySessionAndPR returns the review row for one session PR.
func (s *Store) GetReviewBySessionAndPR(ctx context.Context, id domain.SessionID, prURL string) (domain.Review, bool, error) {
	row, err := s.qr.GetReviewBySessionAndPR(ctx, gen.GetReviewBySessionAndPRParams{SessionID: id, PRURL: prURL})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Review{}, false, nil
	}
	if err != nil {
		return domain.Review{}, false, fmt.Errorf("get review for session %s pr %s: %w", id, prURL, err)
	}
	return reviewFromRow(row), true, nil
}

// ListReviewsBySession returns all per-PR review rows for a worker session.
func (s *Store) ListReviewsBySession(ctx context.Context, id domain.SessionID) ([]domain.Review, error) {
	rows, err := s.qr.ListReviewsBySession(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("list reviews for session %s: %w", id, err)
	}
	out := make([]domain.Review, 0, len(rows))
	for _, row := range rows {
		out = append(out, reviewFromRow(row))
	}
	return out, nil
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
		TargetSha: r.TargetSHA,
		Status:    r.Status,
		Verdict:   r.Verdict,
		Body:      r.Body,
		CreatedAt: r.CreatedAt,
	})
}

// UpdateReviewRunResult sets the status/verdict/body of a running review pass.
func (s *Store) UpdateReviewRunResult(ctx context.Context, id string, status domain.ReviewRunStatus, verdict domain.ReviewVerdict, body string) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := s.qw.UpdateReviewRunResult(ctx, gen.UpdateReviewRunResultParams{
		Status:  status,
		Verdict: verdict,
		Body:    body,
		ID:      id,
	})
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// GetReviewRun returns one review pass by id.
func (s *Store) GetReviewRun(ctx context.Context, id string) (domain.ReviewRun, bool, error) {
	row, err := s.qr.GetReviewRun(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ReviewRun{}, false, nil
	}
	if err != nil {
		return domain.ReviewRun{}, false, fmt.Errorf("get review run %s: %w", id, err)
	}
	return reviewRunFromRow(row), true, nil
}

// GetReviewRunBySessionPRAndSHA returns the most recent review pass for one
// session PR at a specific commit.
func (s *Store) GetReviewRunBySessionPRAndSHA(ctx context.Context, id domain.SessionID, prURL, targetSHA string) (domain.ReviewRun, bool, error) {
	row, err := s.qr.GetReviewRunBySessionPRAndSHA(ctx, gen.GetReviewRunBySessionPRAndSHAParams{SessionID: id, PRURL: prURL, TargetSha: targetSHA})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ReviewRun{}, false, nil
	}
	if err != nil {
		return domain.ReviewRun{}, false, fmt.Errorf("get review run for session %s pr %s sha %s: %w", id, prURL, targetSHA, err)
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

// ListReviewRunsBySessionAndPR returns review passes for one session PR.
func (s *Store) ListReviewRunsBySessionAndPR(ctx context.Context, id domain.SessionID, prURL string) ([]domain.ReviewRun, error) {
	rows, err := s.qr.ListReviewRunsBySessionAndPR(ctx, gen.ListReviewRunsBySessionAndPRParams{SessionID: id, PRURL: prURL})
	if err != nil {
		return nil, fmt.Errorf("list review runs for session %s pr %s: %w", id, prURL, err)
	}
	out := make([]domain.ReviewRun, 0, len(rows))
	for _, row := range rows {
		out = append(out, reviewRunFromRow(row))
	}
	return out, nil
}

func reviewFromRow(r gen.Review) domain.Review {
	return domain.Review{
		ID:               r.ID,
		SessionID:        r.SessionID,
		ProjectID:        r.ProjectID,
		Harness:          r.Harness,
		PRURL:            r.PRURL,
		ReviewerHandleID: r.ReviewerHandleID,
		CreatedAt:        r.CreatedAt,
		UpdatedAt:        r.UpdatedAt,
	}
}

func reviewRunFromRow(r gen.ReviewRun) domain.ReviewRun {
	return domain.ReviewRun{
		ID:        r.ID,
		ReviewID:  r.ReviewID,
		SessionID: r.SessionID,
		Harness:   r.Harness,
		PRURL:     r.PRURL,
		TargetSHA: r.TargetSha,
		Status:    r.Status,
		Verdict:   r.Verdict,
		Body:      r.Body,
		CreatedAt: r.CreatedAt,
	}
}
