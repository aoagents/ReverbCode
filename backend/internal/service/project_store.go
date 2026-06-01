package service

import (
	"context"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// ProjectStore is the durable project persistence surface required by ProjectService.
type ProjectStore interface {
	ListProjects(ctx context.Context) ([]domain.ProjectRecord, error)
	GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error)
	FindProjectByPath(ctx context.Context, path string) (domain.ProjectRecord, bool, error)
	UpsertProject(ctx context.Context, row domain.ProjectRecord) error
	ArchiveProject(ctx context.Context, id string, at time.Time) (bool, error)
}
