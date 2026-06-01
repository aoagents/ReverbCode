package store

import (
	"context"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// ListPRFactsForSession returns every PR snapshot owned by a session, newest
// first, with unresolved-review-comment presence folded in per PR.
func (s *Store) ListPRFactsForSession(ctx context.Context, id domain.SessionID) ([]domain.PRFacts, error) {
	rows, err := s.qr.ListPRFactsBySession(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("list pr facts for %s: %w", id, err)
	}
	facts := make([]domain.PRFacts, 0, len(rows))
	for _, r := range rows {
		state := r.PrState
		facts = append(facts, domain.PRFacts{
			URL:            r.Url,
			Number:         int(r.Number),
			Exists:         true,
			Draft:          state == domain.PRStateDraft,
			Merged:         state == domain.PRStateMerged,
			Closed:         state == domain.PRStateClosed,
			CI:             r.CiState,
			Review:         r.ReviewDecision,
			Mergeability:   r.Mergeability,
			ReviewComments: r.ReviewComments,
		})
	}
	return facts, nil
}
