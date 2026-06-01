package store

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// ListPRFactsForSession returns every PR snapshot owned by a session, newest
// first, with unresolved-review-comment presence folded in per PR.
func (s *Store) ListPRFactsForSession(ctx context.Context, id domain.SessionID) ([]domain.PRFacts, error) {
	rows, err := s.ListPRsBySession(ctx, string(id))
	if err != nil {
		return nil, err
	}
	facts := make([]domain.PRFacts, 0, len(rows))
	for _, r := range rows {
		f := domain.PRFacts{
			URL: r.URL, Number: r.Number, Exists: true,
			Draft: r.Draft, Merged: r.Merged, Closed: r.Closed,
			CI: r.CI, Review: r.Review, Mergeability: r.Mergeability,
		}
		comments, err := s.ListPRComments(ctx, r.URL)
		if err != nil {
			return nil, err
		}
		for _, c := range comments {
			if !c.Resolved {
				f.ReviewComments = true
				break
			}
		}
		facts = append(facts, f)
	}
	return facts, nil
}
