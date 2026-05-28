package ports

import (
	"context"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// LifecycleManager is the inbound contract we implement. Every Apply* method
// runs the same synchronous pipeline: load canonical -> pure decide -> diff ->
// persist (merge-patch) -> if the status transitioned, fire reactions. The LCM
// never polls; observers (SCM poller, reaper, activity ingest) call in.
//
// Concurrency: the LCM serialises per session, so concurrent Apply* calls for
// the same session do not race the load/decide/persist read-modify-write.
type LifecycleManager interface {
	// Raw-fact entrypoints (each runs decide internally).
	ApplySCMObservation(ctx context.Context, id domain.SessionID, f SCMFacts) error
	ApplyRuntimeObservation(ctx context.Context, id domain.SessionID, f RuntimeFacts) error
	ApplyActivitySignal(ctx context.Context, id domain.SessionID, s ActivitySignal) error

	// Mutation outcomes reported by the Session Manager.
	OnSpawnCompleted(ctx context.Context, id domain.SessionID, o SpawnOutcome) error
	OnKillRequested(ctx context.Context, id domain.SessionID, r KillReason) error

	// Reaper heartbeat that drives duration-based escalation (a non-polling
	// LCM can't wake itself to fire a "30m elapsed" escalation).
	TickEscalations(ctx context.Context, now time.Time) error
}

// SessionManager is the inbound contract called by the API layer and CLI. It
// owns explicit mutations (spawn/kill/restore/cleanup) and never derives or
// writes observed state directly — it routes outcomes to the LCM.
type SessionManager interface {
	Spawn(ctx context.Context, cfg SpawnConfig) (domain.Session, error)
	Kill(ctx context.Context, id domain.SessionID, opts KillOptions) (KillResult, error)
	List(ctx context.Context, project domain.ProjectID) ([]domain.Session, error)
	Get(ctx context.Context, id domain.SessionID) (domain.Session, error)
	Send(ctx context.Context, id domain.SessionID, message string) error
	Restore(ctx context.Context, id domain.SessionID) (domain.Session, error)
	Cleanup(ctx context.Context, project domain.ProjectID) (CleanupResult, error)
}

type SpawnConfig struct {
	ProjectID  domain.ProjectID
	IssueID    domain.IssueID
	Kind       domain.SessionKind
	Branch     string
	Prompt     string
	AgentRules string
	// OpenTerminal is reserved for a later lane (open a terminal tab on spawn).
	// Spawn does NOT honor it yet — setting it has no effect.
	OpenTerminal bool
}

type KillOptions struct {
	Reason LifecycleKillReason
	Detail string
}

type KillResult struct {
	ID             domain.SessionID
	WorkspaceFreed bool
}

type CleanupResult struct {
	Cleaned []domain.SessionID
	Skipped []domain.SessionID // e.g. paths that still held uncommitted work
}

// ProjectManager is the inbound contract the API layer calls for every project
// operation. The HTTP controllers see ONLY this interface — they never reach
// past it to the registry, LCM, workspace adapter, or SCM. Whether a method
// touches the global config registry, the LCM (to stop sessions on remove),
// or an adapter (to destroy worktrees) is a private concern of the impl.
type ProjectManager interface {
	List(ctx context.Context) ([]domain.ProjectSummary, error)
	Get(ctx context.Context, id domain.ProjectID) (GetProjectResult, error)
	Add(ctx context.Context, in AddProjectInput) (domain.Project, error)
	UpdateConfig(ctx context.Context, id domain.ProjectID, patch UpdateProjectConfigInput) (domain.Project, error)
	Remove(ctx context.Context, id domain.ProjectID) (RemoveProjectResult, error)
	Repair(ctx context.Context, id domain.ProjectID) (domain.Project, error)
	Reload(ctx context.Context) (ReloadResult, error)
}
