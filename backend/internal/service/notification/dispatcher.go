package notification

import "context"

// Dispatcher is the deliberately tiny v1 delivery boundary.
type Dispatcher interface {
	Dispatch(ctx context.Context, n Notification) error
}

// DashboardPublisher publishes created notifications to dashboard transports.
type DashboardPublisher interface {
	Publish(ctx context.Context, n Notification) error
}

// DashboardDispatcher forwards created notifications to a dashboard publisher.
type DashboardDispatcher struct {
	publisher DashboardPublisher
}

// NewDashboardDispatcher builds a dispatcher around publisher. A nil publisher
// makes dispatch a no-op, which is the v1 daemon default until SSE is added.
func NewDashboardDispatcher(publisher DashboardPublisher) DashboardDispatcher {
	return DashboardDispatcher{publisher: publisher}
}

// Dispatch sends n to the dashboard publisher when one is configured.
func (d DashboardDispatcher) Dispatch(ctx context.Context, n Notification) error {
	if d.publisher == nil {
		return nil
	}
	return d.publisher.Publish(ctx, n)
}
