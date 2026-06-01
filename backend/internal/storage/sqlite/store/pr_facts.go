package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// GetDisplayPRFactsForSession returns the PR snapshot that should represent a
// session in derived display status: active PRs first, otherwise the newest
// historical PR. ok=false means the session has no associated PRs.
func (s *Store) GetDisplayPRFactsForSession(ctx context.Context, id domain.SessionID) (domain.PRFacts, bool, error) {
	r, err := s.qr.GetDisplayPRFactsBySession(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.PRFacts{}, false, nil
	}
	if err != nil {
		return domain.PRFacts{}, false, fmt.Errorf("display pr facts for %s: %w", id, err)
	}
	return prFactsFromGen(r), true, nil
}

func prFactsFromGen(r gen.GetDisplayPRFactsBySessionRow) domain.PRFacts {
	state := r.PrState
	return domain.PRFacts{
		URL:            r.Url,
		Number:         int(r.Number),
		Draft:          state == domain.PRStateDraft,
		Merged:         state == domain.PRStateMerged,
		Closed:         state == domain.PRStateClosed,
		CI:             r.CiState,
		Review:         r.ReviewDecision,
		Mergeability:   r.Mergeability,
		ReviewComments: r.ReviewComments,
	}
}
