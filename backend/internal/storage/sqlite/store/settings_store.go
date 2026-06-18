package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// GetAgentDefaults returns the app-wide spawn defaults. ok=false means the
// user has not configured them yet.
func (s *Store) GetAgentDefaults(ctx context.Context) (domain.AgentDefaults, bool, error) {
	row, err := s.qr.GetAgentDefaults(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.AgentDefaults{}, false, nil
	}
	if err != nil {
		return domain.AgentDefaults{}, false, fmt.Errorf("get agent defaults: %w", err)
	}
	return domain.AgentDefaults{
		DefaultWorkerAgent:       row.DefaultWorkerAgent,
		DefaultOrchestratorAgent: row.DefaultOrchestratorAgent,
	}, true, nil
}

// SetAgentDefaults replaces the app-wide spawn defaults.
func (s *Store) SetAgentDefaults(ctx context.Context, defaults domain.AgentDefaults) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.UpsertAgentDefaults(ctx, gen.UpsertAgentDefaultsParams{
		DefaultWorkerAgent:       defaults.DefaultWorkerAgent,
		DefaultOrchestratorAgent: defaults.DefaultOrchestratorAgent,
	}); err != nil {
		return fmt.Errorf("set agent defaults: %w", err)
	}
	return nil
}
