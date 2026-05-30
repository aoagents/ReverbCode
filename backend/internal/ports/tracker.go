package ports

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Tracker is the outbound port for issue trackers (GitHub Issues, GitLab
// Issues, Linear). v1 is read-only:
//
//   - Get returns a normalized snapshot of one issue, used by spawn-bootstrap
//     to hydrate the agent prompt.
//   - List returns a filtered slice of issues in a repo, used when the SM
//     needs to enumerate work (e.g. backlog view, status sweeps).
//   - Preflight verifies the configured credential is actually valid against
//     the provider so daemons fail fast at startup, not at first request.
//
// Mirroring agent lifecycle back onto the tracker (Comment, Transition) is
// deferred to issue #40. The observer / polling loop is deferred to #35.
//
// All v1 providers share this interface. Provider differences (label vs
// state machine vs close reason) are absorbed inside each adapter via
// domain.NormalizedIssueState. Fields on domain.Issue exist only when every
// provider can populate them; richer per-provider metadata belongs behind a
// separate port.
type Tracker interface {
	Get(ctx context.Context, id domain.TrackerID) (domain.Issue, error)
	List(ctx context.Context, repo domain.TrackerRepo, filter domain.ListFilter) ([]domain.Issue, error)
	Preflight(ctx context.Context) error
}
