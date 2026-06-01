package ports

import (
	"context"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// LifecycleManager is the inbound contract the engine implements. Observers
// (reaper, SCM poller, activity hooks) and the Session Manager call in; the LCM
// is the sole writer of canonical transitions and the only place reactions fire.
type LifecycleManager interface {
	ApplyRuntimeObservation(ctx context.Context, id domain.SessionID, f RuntimeFacts) error
	ApplyActivitySignal(ctx context.Context, id domain.SessionID, s ActivitySignal) error
	ApplyPRObservation(ctx context.Context, id domain.SessionID, o PRObservation) error

	// OnSpawnCompleted marks a session live and records its handles. It works for
	// a fresh spawn (not_started -> live) and a restore (terminal -> reopened).
	OnSpawnCompleted(ctx context.Context, id domain.SessionID, o SpawnOutcome) error
	OnKillRequested(ctx context.Context, id domain.SessionID, reason domain.TerminationReason) error

	// TickEscalations fires the duration-based escalations the synchronous LCM
	// can't wake itself for; the reaper calls it on a timer.
	TickEscalations(ctx context.Context, now time.Time) error
	// RunningSessions snapshots every non-terminal session for the reaper to probe.
	RunningSessions(ctx context.Context) ([]domain.SessionRecord, error)
}

// SessionManager is the inbound contract the API/CLI call for explicit
// mutations. It drives the runtime/agent/workspace plugins and routes canonical
// writes to the LCM.
type SessionManager interface {
	Spawn(ctx context.Context, cfg SpawnConfig) (domain.Session, error)
	Kill(ctx context.Context, id domain.SessionID, reason domain.TerminationReason) (freed bool, err error)
	Restore(ctx context.Context, id domain.SessionID) (domain.Session, error)
	List(ctx context.Context, project domain.ProjectID) ([]domain.Session, error)
	Get(ctx context.Context, id domain.SessionID) (domain.Session, error)
	Send(ctx context.Context, id domain.SessionID, message string) error
	Cleanup(ctx context.Context, project domain.ProjectID) ([]domain.SessionID, error)
}

// SpawnConfig is the request to start a new session: which project/issue, which
// agent harness, and the branch/prompt/rules the agent launches with.
type SpawnConfig struct {
	ProjectID  domain.ProjectID
	IssueID    domain.IssueID
	Kind       domain.SessionKind
	Harness    domain.AgentHarness
	Branch     string
	Prompt     string
	AgentRules string
}
