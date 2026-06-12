package notification

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Store is the notification service's read persistence surface.
type Store interface {
	ListUnreadNotifications(ctx context.Context, limit int) ([]domain.NotificationRecord, error)
}
