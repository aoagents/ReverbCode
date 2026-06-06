package notification

import (
	"context"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Store is the local durable-facts boundary used by the notification service.
// Implementations must not perform network calls and must rely on SQLite
// triggers for notification CDC.
type Store interface {
	UpsertNotification(ctx context.Context, n domain.Notification) (domain.Notification, bool, error)
	ResolveNotifications(ctx context.Context, filter domain.NotificationResolveFilter, resolvedAt time.Time) (int, error)

	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
	GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error)
	ListPRsBySession(ctx context.Context, id domain.SessionID) ([]domain.PullRequest, error)
	ListChecks(ctx context.Context, prURL string) ([]domain.PullRequestCheck, error)
	ListPRComments(ctx context.Context, prURL string) ([]domain.PullRequestComment, error)
	ListPRReviewThreads(ctx context.Context, prURL string) ([]domain.PullRequestReviewThread, error)
}
