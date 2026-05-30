// Package session implements ports.SessionManager: the explicit-mutation half
// of the lane. The SM is impure plumbing — it drives the Runtime/Agent/Workspace
// plugins to create and tear down sessions, and routes mutation commands and
// outcomes to the LCM (OnSpawnInitiated / OnSpawnCompleted / OnKillRequested).
//
// It NEVER writes sessions directly: observed transitions and explicit
// canonical mutations are the LCM's job under the Writer contract. The SM is the
// single producer of the derived display status, attached on read in List/Get
// and never persisted.
package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ErrNotFound is returned by Get/Restore when no record exists for the id.
var ErrNotFound = errors.New("session: not found")

// ErrNotRestorable is returned by Restore when the session is not torn down.
// Restoring a live session would spin up a second runtime/workspace for the same
// id, duplicating the agent and risking data loss.
var ErrNotRestorable = errors.New("session: not restorable (not terminal)")

// ErrIncompleteTeardownMetadata is returned when a record's teardown handles are
// missing (empty workspace path or runtime handle), so calling a real adapter's
// Destroy could act on empty args — an unsafe delete. The teardown is skipped.
var ErrIncompleteTeardownMetadata = errors.New("session: incomplete teardown metadata")

// Env vars a spawned process reads to learn who it is (distillation §5.4).
const (
	EnvSessionID = "AO_SESSION_ID"
	EnvProjectID = "AO_PROJECT_ID"
	EnvIssueID   = "AO_ISSUE_ID"
)

// Manager implements ports.SessionManager against the outbound ports. Every
// dependency is an interface so the SM runs entirely against fakes in tests.
type Manager struct {
	runtime   ports.Runtime
	agent     ports.Agent
	workspace ports.Workspace
	store     ports.LifecycleStore
	messenger ports.AgentMessenger
	lcm       ports.LifecycleManager

	clock func() time.Time
	newID func(ports.SpawnConfig) domain.SessionID
}

var _ ports.SessionManager = (*Manager)(nil)

// Deps groups the SM's collaborators. Clock and NewID are optional (defaulted)
// so production wiring only supplies the ports.
type Deps struct {
	Runtime   ports.Runtime
	Agent     ports.Agent
	Workspace ports.Workspace
	Store     ports.LifecycleStore
	Messenger ports.AgentMessenger
	Lifecycle ports.LifecycleManager

	Clock func() time.Time
	NewID func(ports.SpawnConfig) domain.SessionID
}

func New(d Deps) *Manager {
	m := &Manager{
		runtime:   d.Runtime,
		agent:     d.Agent,
		workspace: d.Workspace,
		store:     d.Store,
		messenger: d.Messenger,
		lcm:       d.Lifecycle,
		clock:     d.Clock,
		newID:     d.NewID,
	}
	if m.clock == nil {
		m.clock = time.Now
	}
	if m.newID == nil {
		m.newID = defaultNewID
	}
	return m
}

// ---- Spawn ----

// Spawn runs the create pipeline in spec order: workspace -> runtime -> route
// seed command to the LCM -> report completion to the LCM. The record is seeded LATE (after the runtime is up), so a
// failure before the seed leaves no record for Cleanup to reclaim — hence each
// step eagerly rolls back the steps that already succeeded.
func (m *Manager) Spawn(ctx context.Context, cfg ports.SpawnConfig) (domain.Session, error) {
	id := m.newID(cfg)
	if _, ok, err := m.store.Get(ctx, id); err != nil {
		return domain.Session{}, fmt.Errorf("spawn %s: check existing: %w", id, err)
	} else if ok {
		return domain.Session{}, fmt.Errorf("spawn %s: already exists", id)
	}

	ws, err := m.workspace.Create(ctx, ports.WorkspaceConfig{
		ProjectID: cfg.ProjectID,
		SessionID: id,
		Branch:    cfg.Branch,
	})
	if err != nil {
		return domain.Session{}, fmt.Errorf("spawn %s: workspace create: %w", id, err)
	}

	agentCfg := ports.AgentConfig{SessionID: id, WorkspacePath: ws.Path, Prompt: buildPrompt(cfg)}
	handle, err := m.runtime.Create(ctx, ports.RuntimeConfig{
		SessionID:     id,
		WorkspacePath: ws.Path,
		LaunchCommand: m.agent.GetLaunchCommand(agentCfg),
		Env:           spawnEnv(m.agent.GetEnvironment(agentCfg), id, cfg.ProjectID, cfg.IssueID),
	})
	if err != nil {
		m.rollbackWorkspace(ctx, ws) // nothing seeded yet
		return domain.Session{}, fmt.Errorf("spawn %s: runtime create: %w", id, err)
	}

	if err := m.lcm.OnSpawnInitiated(ctx, seedRecord(id, cfg, m.clock())); err != nil {
		m.rollbackRuntime(ctx, handle)
		m.rollbackWorkspace(ctx, ws)
		return domain.Session{}, fmt.Errorf("spawn %s: on spawn initiated: %w", id, err)
	}

	// Prompt is persisted via OnSpawnCompleted -> spawnMetadata so a later Restore
	// can fall back to a fresh launch if the agent's native session id was never
	// captured (the capture path is a separate hook that may never have run).
	outcome := ports.SpawnOutcome{Branch: ws.Branch, WorkspacePath: ws.Path, RuntimeHandle: handle, Prompt: agentCfg.Prompt}
	if err := m.lcm.OnSpawnCompleted(ctx, id, outcome); err != nil {
		// The record is seeded but the runtime/workspace are about to be torn
		// down. The store has no delete, so route the orphan to a terminal
		// errored state (best effort) rather than strand a phantom "spawning".
		_ = m.lcm.OnKillRequested(ctx, id, ports.KillReason{Kind: ports.KillError, Detail: "spawn completion failed"})
		m.rollbackRuntime(ctx, handle)
		m.rollbackWorkspace(ctx, ws)
		return domain.Session{}, fmt.Errorf("spawn %s: on spawn completed: %w", id, err)
	}

	return m.Get(ctx, id)
}

// rollback* are best-effort: the caller already has the originating failure, and
// there is no logger at this layer, so a secondary teardown error is dropped
// rather than masking the real cause.
func (m *Manager) rollbackWorkspace(ctx context.Context, ws ports.WorkspaceInfo) {
	_ = m.workspace.Destroy(ctx, ws)
}

func (m *Manager) rollbackRuntime(ctx context.Context, h ports.RuntimeHandle) {
	_ = m.runtime.Destroy(ctx, h)
}

// ---- Kill ----

// Kill records terminal intent with the LCM FIRST, then tears down the runtime
// and workspace. There is no separate Agent stop: the agent runs inside the
// runtime, so Runtime.Destroy stops it. The workspace teardown honors the
// worktree-remove safety — a refusal (path still registered after prune, so it
// may hold uncommitted work) surfaces as an error with WorkspaceFreed=false and
// is never forced.
func (m *Manager) Kill(ctx context.Context, id domain.SessionID, opts ports.KillOptions) (ports.KillResult, error) {
	rec, ok, err := m.store.Get(ctx, id)
	if err != nil {
		return ports.KillResult{ID: id}, fmt.Errorf("kill %s: %w", id, err)
	}
	if !ok {
		// Already gone: benign race, mirrors LCM.OnKillRequested's no-op.
		return ports.KillResult{ID: id}, nil
	}
	meta, err := m.store.GetMetadata(ctx, id)
	if err != nil {
		return ports.KillResult{ID: id}, fmt.Errorf("kill %s: metadata: %w", id, err)
	}

	// Validate the teardown handles BEFORE recording intent or touching an
	// adapter: a corrupted/partially-seeded record with empty handles must never
	// reach Destroy (empty path / handle could be an unsafe delete).
	rtHandle := runtimeHandle(meta)
	wsInfo := workspaceInfo(rec, meta)
	if !validRuntimeHandle(rtHandle) {
		return ports.KillResult{ID: id}, fmt.Errorf("kill %s: %w: runtime handle", id, ErrIncompleteTeardownMetadata)
	}
	if !validWorkspaceInfo(wsInfo) {
		return ports.KillResult{ID: id}, fmt.Errorf("kill %s: %w: workspace path", id, ErrIncompleteTeardownMetadata)
	}

	if err := m.lcm.OnKillRequested(ctx, id, ports.KillReason{Kind: opts.Reason, Detail: opts.Detail}); err != nil {
		return ports.KillResult{ID: id}, fmt.Errorf("kill %s: on kill requested: %w", id, err)
	}
	if err := m.runtime.Destroy(ctx, rtHandle); err != nil {
		return ports.KillResult{ID: id}, fmt.Errorf("kill %s: runtime destroy: %w", id, err)
	}
	if err := m.workspace.Destroy(ctx, wsInfo); err != nil {
		return ports.KillResult{ID: id, WorkspaceFreed: false}, fmt.Errorf("kill %s: workspace destroy: %w", id, err)
	}
	return ports.KillResult{ID: id, WorkspaceFreed: true}, nil
}

// ---- read-model ----

// List builds the read-model for a project: stored records with the display
// status derived on read. The SM is the single producer of that status.
func (m *Manager) List(ctx context.Context, project domain.ProjectID) ([]domain.Session, error) {
	recs, err := m.store.List(ctx, project)
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", project, err)
	}
	out := make([]domain.Session, 0, len(recs))
	for _, rec := range recs {
		out = append(out, toSession(rec))
	}
	return out, nil
}

func (m *Manager) Get(ctx context.Context, id domain.SessionID) (domain.Session, error) {
	rec, ok, err := m.store.Get(ctx, id)
	if err != nil {
		return domain.Session{}, fmt.Errorf("get %s: %w", id, err)
	}
	if !ok {
		return domain.Session{}, fmt.Errorf("get %s: %w", id, ErrNotFound)
	}
	return toSession(rec), nil
}

// ---- Send ----

// Send routes a message to the running agent through the AgentMessenger, which
// busy-detects and verifies delivery.
func (m *Manager) Send(ctx context.Context, id domain.SessionID, message string) error {
	if err := m.messenger.Send(ctx, id, message); err != nil {
		return fmt.Errorf("send %s: %w", id, err)
	}
	return nil
}

// ---- Restore ----

// Restore relaunches a previously torn-down session in its workspace. The
// fallible I/O (workspace restore + runtime create) runs first so a failure
// touches no canonical state and never destroys the worktree (it may hold the
// agent's prior work). Only once the runtime is up do we reopen the lifecycle:
// resetting a terminal session is an explicit mutation routed to the LCM (the
// LCM's observe path would never resurrect a terminal session), and the PR axis
// is cleared. OnSpawnCompleted then flips the runtime to alive.
func (m *Manager) Restore(ctx context.Context, id domain.SessionID) (domain.Session, error) {
	rec, ok, err := m.store.Get(ctx, id)
	if err != nil {
		return domain.Session{}, fmt.Errorf("restore %s: %w", id, err)
	}
	if !ok {
		return domain.Session{}, fmt.Errorf("restore %s: %w", id, ErrNotFound)
	}
	// Only a torn-down session may be restored. Reopening a live one would spawn a
	// duplicate runtime/workspace for the same id and reset its lifecycle.
	if !isTerminalSession(rec.Lifecycle.Session.State) {
		return domain.Session{}, fmt.Errorf("restore %s: %w", id, ErrNotRestorable)
	}
	meta, err := m.store.GetMetadata(ctx, id)
	if err != nil {
		return domain.Session{}, fmt.Errorf("restore %s: metadata: %w", id, err)
	}

	// Resume is only possible with the agent's captured session id; without it we
	// fall back to a fresh launch using the seeded prompt persisted at spawn time
	// (the agent's id-capture path is a separate hook that may never have run, so
	// "no id" is the common case rather than an error). If neither is available
	// there is nothing to relaunch from — fail early, before any I/O.
	agentSessionID := meta[lifecycle.MetaAgentSessionID]
	seededPrompt := meta[lifecycle.MetaPrompt]
	if agentSessionID == "" && seededPrompt == "" {
		return domain.Session{}, fmt.Errorf("restore %s: no agent session id or seeded prompt (cannot resume or relaunch)", id)
	}

	ws, err := m.workspace.Restore(ctx, ports.WorkspaceConfig{
		ProjectID: rec.ProjectID,
		SessionID: id,
		Branch:    meta[lifecycle.MetaBranch],
	})
	if err != nil {
		return domain.Session{}, fmt.Errorf("restore %s: workspace restore: %w", id, err)
	}

	agentCfg := ports.AgentConfig{SessionID: id, WorkspacePath: ws.Path, Prompt: seededPrompt}
	launchCommand := m.agent.GetRestoreCommand(agentSessionID)
	if agentSessionID == "" {
		launchCommand = m.agent.GetLaunchCommand(agentCfg)
	}
	handle, err := m.runtime.Create(ctx, ports.RuntimeConfig{
		SessionID:     id,
		WorkspacePath: ws.Path,
		LaunchCommand: launchCommand,
		Env:           spawnEnv(m.agent.GetEnvironment(agentCfg), id, rec.ProjectID, rec.IssueID),
	})
	if err != nil {
		return domain.Session{}, fmt.Errorf("restore %s: runtime create: %w", id, err)
	}

	// Past this point the runtime is live: a failure must tear it back down (but
	// never the workspace, which holds the agent's prior work) so we don't strand
	// a process while parking the session in a terminal lifecycle.
	reopen := rec
	reopen.Lifecycle.Session = domain.SessionSubstate{State: domain.SessionNotStarted, Reason: domain.ReasonSpawnRequested}
	reopen.Lifecycle.PR = domain.PRSubstate{State: domain.PRNone, Reason: domain.PRReasonClearedOnRestore}
	reopen.Lifecycle.Runtime = domain.RuntimeSubstate{State: domain.RuntimeUnknown, Reason: domain.RuntimeReasonSpawnIncomplete}
	reopen.Lifecycle.Detecting = nil
	if err := m.lcm.OnSpawnInitiated(ctx, reopen); err != nil {
		m.rollbackRuntime(ctx, handle)
		return domain.Session{}, fmt.Errorf("restore %s: on spawn initiated: %w", id, err)
	}

	outcome := ports.SpawnOutcome{
		Branch:         ws.Branch,
		WorkspacePath:  ws.Path,
		RuntimeHandle:  handle,
		AgentSessionID: agentSessionID,
		Prompt:         seededPrompt,
	}
	if err := m.lcm.OnSpawnCompleted(ctx, id, outcome); err != nil {
		m.rollbackRuntime(ctx, handle)
		// Re-upsert the original record to undo the reopen; the store will
		// assign the next revision.
		if revertErr := m.lcm.OnSpawnInitiated(ctx, rec); revertErr != nil {
			return domain.Session{}, fmt.Errorf("restore %s: revert after spawn completed failure: %w (original error: %v)", id, revertErr, err)
		}
		if len(rec.Metadata) > 0 {
			if revertErr := m.store.PatchMetadata(ctx, id, rec.Metadata); revertErr != nil {
				return domain.Session{}, fmt.Errorf("restore %s: revert metadata after spawn completed failure: %w (original error: %v)", id, revertErr, err)
			}
		}
		return domain.Session{}, fmt.Errorf("restore %s: on spawn completed: %w", id, err)
	}
	return m.Get(ctx, id)
}

// ---- Cleanup ----

// Cleanup reclaims the workspaces of terminal sessions in a project. A workspace
// whose teardown is refused by the worktree-remove safety (uncommitted work) is
// skipped, never forced. Runtime teardown is best-effort (a terminal session's
// runtime is usually already gone); the workspace result decides cleaned/skipped.
func (m *Manager) Cleanup(ctx context.Context, project domain.ProjectID) (ports.CleanupResult, error) {
	recs, err := m.store.List(ctx, project)
	if err != nil {
		return ports.CleanupResult{}, fmt.Errorf("cleanup %s: %w", project, err)
	}
	var res ports.CleanupResult
	for _, rec := range recs {
		if !isTerminalSession(rec.Lifecycle.Session.State) {
			continue
		}
		meta, err := m.store.GetMetadata(ctx, rec.ID)
		if err != nil {
			return res, fmt.Errorf("cleanup %s: metadata %s: %w", project, rec.ID, err)
		}
		wsInfo := workspaceInfo(rec, meta)
		if !validWorkspaceInfo(wsInfo) {
			// No workspace path to reclaim — skip rather than hand empty args to a
			// real adapter's Destroy (an unsafe delete).
			res.Skipped = append(res.Skipped, rec.ID)
			continue
		}
		if rtHandle := runtimeHandle(meta); validRuntimeHandle(rtHandle) {
			_ = m.runtime.Destroy(ctx, rtHandle) // best effort; usually already gone
		}
		if err := m.workspace.Destroy(ctx, wsInfo); err != nil {
			res.Skipped = append(res.Skipped, rec.ID)
			continue
		}
		res.Cleaned = append(res.Cleaned, rec.ID)
	}
	return res, nil
}

// ---- helpers ----

func toSession(rec domain.SessionRecord) domain.Session {
	return domain.Session{SessionRecord: rec, Status: domain.DeriveLegacyStatus(rec.Lifecycle)}
}

func isTerminalSession(s domain.SessionState) bool {
	return s == domain.SessionDone || s == domain.SessionTerminated
}

// buildPrompt assembles the spawn prompt from the explicit config only; the full
// 3-layer assembly (base protocol + config-derived + user rules) lands later.
func buildPrompt(cfg ports.SpawnConfig) string {
	switch {
	case cfg.AgentRules == "":
		return cfg.Prompt
	case cfg.Prompt == "":
		return cfg.AgentRules
	default:
		return cfg.Prompt + "\n\n" + cfg.AgentRules
	}
}

// spawnEnv overlays the AO_* identity vars onto the agent's environment without
// mutating the map the agent returned.
func spawnEnv(base map[string]string, id domain.SessionID, project domain.ProjectID, issue domain.IssueID) map[string]string {
	env := make(map[string]string, len(base)+3)
	for k, v := range base {
		env[k] = v
	}
	env[EnvSessionID] = string(id)
	env[EnvProjectID] = string(project)
	env[EnvIssueID] = string(issue)
	return env
}

func seedRecord(id domain.SessionID, cfg ports.SpawnConfig, now time.Time) domain.SessionRecord {
	return domain.SessionRecord{
		ID:        id,
		ProjectID: cfg.ProjectID,
		IssueID:   cfg.IssueID,
		Kind:      cfg.Kind,
		CreatedAt: now,
		UpdatedAt: now,
		Lifecycle: domain.CanonicalSessionLifecycle{
			Version: domain.LifecycleVersion,
			Session: domain.SessionSubstate{State: domain.SessionNotStarted, Reason: domain.ReasonSpawnRequested},
			Runtime: domain.RuntimeSubstate{State: domain.RuntimeUnknown, Reason: domain.RuntimeReasonSpawnIncomplete},
			PR:      domain.PRSubstate{State: domain.PRNone, Reason: domain.PRReasonNotCreated},
		},
	}
}

// runtimeHandle / workspaceInfo reconstruct teardown handles from the metadata
// the LCM persisted in OnSpawnCompleted (the metadata-key contract is shared
// with the lifecycle package).
func runtimeHandle(meta map[string]string) ports.RuntimeHandle {
	return ports.RuntimeHandle{
		ID:          meta[lifecycle.MetaRuntimeHandleID],
		RuntimeName: meta[lifecycle.MetaRuntimeName],
	}
}

func workspaceInfo(rec domain.SessionRecord, meta map[string]string) ports.WorkspaceInfo {
	return ports.WorkspaceInfo{
		Path:      meta[lifecycle.MetaWorkspacePath],
		Branch:    meta[lifecycle.MetaBranch],
		SessionID: rec.ID,
		ProjectID: rec.ProjectID,
	}
}

// validRuntimeHandle reports whether the handle identifies a runtime to destroy.
// An adapter needs the handle id to target the right process; an empty handle
// would be ambiguous, so we refuse to call Destroy with one.
func validRuntimeHandle(h ports.RuntimeHandle) bool {
	return h.ID != ""
}

// validWorkspaceInfo reports whether there is a concrete path to reclaim. An
// empty path handed to a worktree-remove could resolve to an unsafe target.
func validWorkspaceInfo(w ports.WorkspaceInfo) bool {
	return w.Path != ""
}

func defaultNewID(cfg ports.SpawnConfig) domain.SessionID {
	base := string(cfg.IssueID)
	if base == "" {
		base = string(cfg.Kind)
	}
	if base == "" {
		base = "session"
	}
	return domain.SessionID(base + "-" + randHex(4))
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b)
}
