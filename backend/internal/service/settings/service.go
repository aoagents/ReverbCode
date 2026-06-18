package settings

import (
	"context"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// Store is the persistence surface for app-wide user settings.
type Store interface {
	GetAgentDefaults(ctx context.Context) (domain.AgentDefaults, bool, error)
	SetAgentDefaults(ctx context.Context, defaults domain.AgentDefaults) error
}

// Service owns validation and persistence for app-wide user settings.
type Service struct {
	store Store
}

// New wires a settings service over a store.
func New(store Store) *Service {
	return &Service{store: store}
}

// GetAgentDefaults returns configured defaults. Missing settings return the
// zero value so callers can distinguish first-run setup by Complete().
func (s *Service) GetAgentDefaults(ctx context.Context) (domain.AgentDefaults, error) {
	defaults, _, err := s.store.GetAgentDefaults(ctx)
	if err != nil {
		return domain.AgentDefaults{}, fmt.Errorf("get agent defaults: %w", err)
	}
	return defaults, nil
}

// SetAgentDefaults validates and persists app-wide agent defaults.
func (s *Service) SetAgentDefaults(ctx context.Context, defaults domain.AgentDefaults) (domain.AgentDefaults, error) {
	if err := defaults.ValidateComplete(); err != nil {
		return domain.AgentDefaults{}, apierr.Invalid("INVALID_AGENT_DEFAULTS", err.Error(), nil)
	}
	if err := s.store.SetAgentDefaults(ctx, defaults); err != nil {
		return domain.AgentDefaults{}, fmt.Errorf("set agent defaults: %w", err)
	}
	return defaults, nil
}
