package store

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// PRFactsForSession picks the PR that drives display/reaction status — the
// newest non-closed PR, else the newest PR — and folds in whether it has
// unresolved review comments.
func (s *Store) PRFactsForSession(ctx context.Context, id domain.SessionID) (domain.PRFacts, error) {
	rows, err := s.ListPRsBySession(ctx, string(id))
	if err != nil {
		return domain.PRFacts{}, err
	}
	if len(rows) == 0 {
		return domain.PRFacts{}, nil
	}
	pick := rows[0]
	for _, r := range rows {
		if !r.Merged && !r.Closed { // newest non-closed (draft or open)
			pick = r
			break
		}
	}
	facts := domain.PRFacts{
		URL: pick.URL, Number: pick.Number, Exists: true,
		Draft: pick.Draft, Merged: pick.Merged, Closed: pick.Closed,
		CI: pick.CI, Review: pick.Review, Mergeability: pick.Mergeability,
	}
	comments, err := s.ListPRComments(ctx, pick.URL)
	if err != nil {
		return domain.PRFacts{}, err
	}
	for _, c := range comments {
		if !c.Resolved {
			facts.ReviewComments = true
			break
		}
	}
	return facts, nil
}
