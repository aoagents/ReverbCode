package notification

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Store is the notification service's persistence surface.
type Store interface {
	CreateNotification(ctx context.Context, rec domain.NotificationRecord) (domain.NotificationRecord, bool, error)
	ListUnreadNotifications(ctx context.Context, limit int) ([]domain.NotificationRecord, error)
	ListUnreadNotificationsByProject(ctx context.Context, projectID domain.ProjectID, limit int) ([]domain.NotificationRecord, error)
}
