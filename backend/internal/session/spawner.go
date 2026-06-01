package session

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Spawner is the slice of the Session Manager the HTTP controller depends on.
// *Manager satisfies it; tests can substitute a fake without dragging in the
// runtime/workspace/agent collaborators a real Manager needs.
type Spawner interface {
	Spawn(ctx context.Context, cfg ports.SpawnConfig) (domain.Session, error)
}

var _ Spawner = (*Manager)(nil)
