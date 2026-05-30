package ports

import (
	"context"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// LifecycleManager is the inbound contract we implement. Every Apply* method
// runs the same synchronous pipeline: load canonical -> pure decide -> diff ->
// persist (full-row Upsert) -> if the status transitioned, fire reactions. The LCM
// never polls; observers (SCM poller, reaper, activity ingest) call in.
//
// Concurrency: the LCM serialises per session, so concurrent Apply* calls for
// the same session do not race the load/decide/persist read-modify-write.
type LifecycleManager interface {
	// Raw-fact entrypoints (each runs decide internally).
	ApplySCMObservation(ctx context.Context, id domain.SessionID, f SCMFacts) error
	ApplyRuntimeObservation(ctx context.Context, id domain.SessionID, f RuntimeFacts) error
	ApplyActivitySignal(ctx context.Context, id domain.SessionID, s ActivitySignal) error

	// Mutation commands/outcomes reported by the Session Manager.
	OnSpawnInitiated(ctx context.Context, rec domain.SessionRecord) error
	OnSpawnCompleted(ctx context.Context, id domain.SessionID, o SpawnOutcome) error
	OnKillRequested(ctx context.Context, id domain.SessionID, r KillReason) error

	// Reaper heartbeat that drives duration-based escalation (a non-polling
	// LCM can't wake itself to fire a "30m elapsed" escalation).
	TickEscalations(ctx context.Context, now time.Time) error

	// RunningSessions returns a snapshot of every session whose runtime axis is
	// alive. The reaper calls it once per tick to decide whom to probe. It is a
	// read snapshot — the slice and its elements are safe for the caller to
	// iterate without holding any LCM lock — and does not violate the
	// single-writer invariant (the reaper never writes; it reports facts back
	// through ApplyRuntimeObservation).
	RunningSessions(ctx context.Context) ([]domain.SessionRecord, error)
}

// SessionManager is the inbound contract called by the API layer and CLI. It
// owns explicit mutations (spawn/kill/restore/cleanup) and never writes
// sessions directly — it routes mutation commands/outcomes to the LCM.
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
