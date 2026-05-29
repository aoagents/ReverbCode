package ports

import (
	"context"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// SCMProvider is the observation-side provider contract. Implementations own
// provider-specific network/API behavior and return normalized snapshots only;
// they do not import lifecycle/session packages.
type SCMProvider interface {
	Provider() domain.SCMProvider
	ObserveSessions(ctx context.Context, req SCMObserveRequest, cache SCMProviderCache) (SCMObserveResult, error)
}

type SCMObserveRequest struct {
	Subjects []domain.SCMSubject
	Now      time.Time
}

type SCMObserveResult struct {
	Snapshots    []domain.SCMSnapshot
	Subjects     []domain.SCMSubject // updated bindings, e.g. branch -> PR discovery
	PollStates   []domain.SCMPollState
	Diagnostics  []domain.SCMDiagnostic
	RateLimit    *domain.SCMRateLimit
	Unavailable  bool
	ProviderName domain.SCMProvider
}

// SCMProviderCache is the small cache surface providers need for ETags and
// positive branch->PR mappings. SCMStore implements it; tests can inject an
// in-memory cache directly.
type SCMProviderCache interface {
	GetProviderCache(ctx context.Context, key domain.SCMProviderCacheKey) (domain.SCMProviderCacheEntry, bool, error)
	PutProviderCache(ctx context.Context, entry domain.SCMProviderCacheEntry) error
	DeleteProviderCache(ctx context.Context, prefix domain.SCMProviderCachePrefix) error
}

// SCMStore owns durable SCM state. SaveSnapshot assigns monotonic revisions and
// returns changed=false when the semantic hash is unchanged.
type SCMStore interface {
	SCMProviderCache
	UpsertSubject(ctx context.Context, subject domain.SCMSubject) error
	GetSubject(ctx context.Context, sessionID domain.SessionID) (domain.SCMSubject, bool, error)
	ListSubjects(ctx context.Context, filter domain.SCMSubjectFilter) ([]domain.SCMSubject, error)
	DeleteSubject(ctx context.Context, sessionID domain.SessionID) error

	SaveSnapshot(ctx context.Context, snapshot domain.SCMSnapshot) (saved domain.SCMSnapshot, changed bool, err error)
	GetLatestSnapshot(ctx context.Context, sessionID domain.SessionID) (domain.SCMSnapshot, bool, error)
	ListLatestSnapshots(ctx context.Context, project domain.ProjectID) ([]domain.SCMSnapshot, error)

	GetPollState(ctx context.Context, key domain.SCMPollStateKey) (domain.SCMPollState, bool, error)
	PutPollState(ctx context.Context, state domain.SCMPollState) error
}

// SCMCommandProvider executes provider mutations. A command result is an audit
// record, never lifecycle truth; observer refresh must land before the LCM sees
// the state change.
type SCMCommandProvider interface {
	Provider() domain.SCMProvider
	Merge(ctx context.Context, req SCMCommandRequest) (SCMCommandResult, error)
	Close(ctx context.Context, req SCMCommandRequest) (SCMCommandResult, error)
	Comment(ctx context.Context, req SCMCommandRequest) (SCMCommandResult, error)
	Assign(ctx context.Context, req SCMCommandRequest) (SCMCommandResult, error)
	Checkout(ctx context.Context, req SCMCommandRequest) (SCMCommandResult, error)
	Capabilities() SCMCommandCapabilities
}

type SCMCommand string

const (
	SCMCommandMerge    SCMCommand = "merge"
	SCMCommandClose    SCMCommand = "close"
	SCMCommandComment  SCMCommand = "comment"
	SCMCommandAssign   SCMCommand = "assign"
	SCMCommandCheckout SCMCommand = "checkout"
)

type SCMCommandRequest struct {
	Subject       domain.SCMSubject
	ChangeRequest domain.SCMChangeRequestID
	Command       SCMCommand
	Message       string
	Body          string
	Assignees     []string
	MergeMethod   string
	CommitTitle   string
	CommitMessage string
	WorkspacePath string
	Actor         string
	Now           time.Time
}

type SCMCommandResult struct {
	Provider      domain.SCMProvider        `json:"provider"`
	Command       SCMCommand                `json:"command"`
	ChangeRequest domain.SCMChangeRequestID `json:"changeRequest"`
	URL           string                    `json:"url,omitempty"`
	SHA           string                    `json:"sha,omitempty"`
	Message       string                    `json:"message,omitempty"`
	PerformedAt   time.Time                 `json:"performedAt"`
	Diagnostics   []domain.SCMDiagnostic    `json:"diagnostics,omitempty"`
}

type SCMCommandCapabilities struct {
	Merge    bool `json:"merge"`
	Close    bool `json:"close"`
	Comment  bool `json:"comment"`
	Assign   bool `json:"assign"`
	Checkout bool `json:"checkout"`
}

// SCMObserver is the inbound scheduler/refresh contract exposed to API and
// command layers.
type SCMObserver interface {
	Refresh(ctx context.Context, subjects []domain.SCMSubject) error
	RefreshSession(ctx context.Context, sessionID domain.SessionID) error
	Invalidate(ctx context.Context, subject domain.SCMSubject, reason string) error
}
