// Package session implements ports.SessionManager: the explicit-mutation half of
// the lane. It drives the runtime/agent/workspace plugins to create and tear
// down sessions, routes canonical writes to the LCM, and is the single producer
// of the derived display status (attached on read in List/Get).
package session

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Sentinel errors returned by the Session Manager.
var (
	ErrNotFound         = errors.New("session: not found")
	ErrNotRestorable    = errors.New("session: not restorable (not terminal)")
	ErrIncompleteHandle = errors.New("session: incomplete teardown handle")
)

// Env vars a spawned process reads to learn who it is.
const (
	EnvSessionID = "AO_SESSION_ID"
	EnvProjectID = "AO_PROJECT_ID"
	EnvIssueID   = "AO_ISSUE_ID"
)

// Manager implements ports.SessionManager over the outbound ports.
type Manager struct {
	runtime   ports.Runtime
	agent     ports.Agent
	workspace ports.Workspace
	store     ports.SessionStore
	messenger ports.AgentMessenger
	lcm       ports.LifecycleManager
	clock     func() time.Time
}

var _ ports.SessionManager = (*Manager)(nil)

// Deps are the collaborators a Session Manager needs; New wires them together.
type Deps struct {
	Runtime   ports.Runtime
	Agent     ports.Agent
	Workspace ports.Workspace
	Store     ports.SessionStore
	Messenger ports.AgentMessenger
	Lifecycle ports.LifecycleManager
	Clock     func() time.Time
}

// New builds a Session Manager from its dependencies, defaulting the clock to
// time.Now when Deps.Clock is nil.
func New(d Deps) *Manager {
	m := &Manager{
		runtime:   d.Runtime,
		agent:     d.Agent,
		workspace: d.Workspace,
		store:     d.Store,
		messenger: d.Messenger,
		lcm:       d.Lifecycle,
		clock:     d.Clock,
	}
	if m.clock == nil {
		m.clock = time.Now
	}
	return m
}

// Spawn creates the session row (which assigns the "{project}-{n}" id), then the
// workspace and runtime, then reports completion to the LCM. A failure after the
// row exists routes it to a terminal errored state and rolls back what was built.
func (m *Manager) Spawn(ctx context.Context, cfg ports.SpawnConfig) (domain.Session, error) {
	rec, err := m.store.CreateSession(ctx, seedRecord(cfg, m.clock()))
	if err != nil {
		return domain.Session{}, fmt.Errorf("spawn: create: %w", err)
	}
	id := rec.ID

	ws, err := m.workspace.Create(ctx, ports.WorkspaceConfig{ProjectID: cfg.ProjectID, SessionID: id, Branch: cfg.Branch})
	if err != nil {
		m.markErrored(ctx, id)
		return domain.Session{}, fmt.Errorf("spawn %s: workspace: %w", id, err)
	}

	agentCfg := ports.AgentConfig{SessionID: id, WorkspacePath: ws.Path, Prompt: buildPrompt(cfg)}
	handle, err := m.runtime.Create(ctx, ports.RuntimeConfig{
		SessionID:     id,
		WorkspacePath: ws.Path,
		LaunchCommand: m.agent.GetLaunchCommand(agentCfg),
		Env:           spawnEnv(m.agent.GetEnvironment(agentCfg), id, cfg.ProjectID, cfg.IssueID),
	})
	if err != nil {
		_ = m.workspace.Destroy(ctx, ws)
		m.markErrored(ctx, id)
		return domain.Session{}, fmt.Errorf("spawn %s: runtime: %w", id, err)
	}

	outcome := ports.SpawnOutcome{Branch: ws.Branch, WorkspacePath: ws.Path, RuntimeHandle: handle, Prompt: agentCfg.Prompt}
	if err := m.lcm.OnSpawnCompleted(ctx, id, outcome); err != nil {
		_ = m.runtime.Destroy(ctx, handle)
		_ = m.workspace.Destroy(ctx, ws)
		m.markErrored(ctx, id)
		return domain.Session{}, fmt.Errorf("spawn %s: completed: %w", id, err)
	}
	return m.Get(ctx, id)
}

// markErrored best-effort parks an orphaned spawn in a terminal errored state
// (the store has no delete; a phantom "spawning" row is worse than a terminal one).
func (m *Manager) markErrored(ctx context.Context, id domain.SessionID) {
	_ = m.lcm.OnKillRequested(ctx, id, domain.TermErrorInProcess)
}

// Kill records terminal intent with the LCM, then tears down the runtime and
// workspace. A workspace teardown refused by the worktree-remove safety
// (uncommitted work) surfaces as an error with freed=false and is never forced.
func (m *Manager) Kill(ctx context.Context, id domain.SessionID, reason domain.TerminationReason) (bool, error) {
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return false, fmt.Errorf("kill %s: %w", id, err)
	}
	if !ok {
		return false, nil // already gone: benign race
	}
	handle := runtimeHandle(rec.Metadata)
	ws := workspaceInfo(rec)
	if handle.ID == "" || ws.Path == "" {
		return false, fmt.Errorf("kill %s: %w", id, ErrIncompleteHandle)
	}
	if err := m.lcm.OnKillRequested(ctx, id, reason); err != nil {
		return false, fmt.Errorf("kill %s: %w", id, err)
	}
	if err := m.runtime.Destroy(ctx, handle); err != nil {
		return false, fmt.Errorf("kill %s: runtime: %w", id, err)
	}
	if err := m.workspace.Destroy(ctx, ws); err != nil {
		return false, fmt.Errorf("kill %s: workspace: %w", id, err)
	}
	return true, nil
}

// Restore relaunches a torn-down session in its workspace. The fallible I/O runs
// before any canonical write, so a failure never resurrects the row or destroys
// the worktree (it may hold the agent's prior work).
func (m *Manager) Restore(ctx context.Context, id domain.SessionID) (domain.Session, error) {
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return domain.Session{}, fmt.Errorf("restore %s: %w", id, err)
	}
	if !ok {
		return domain.Session{}, fmt.Errorf("restore %s: %w", id, ErrNotFound)
	}
	if !isTerminal(rec.Lifecycle.Session.State) {
		return domain.Session{}, fmt.Errorf("restore %s: %w", id, ErrNotRestorable)
	}
	meta := rec.Metadata
	if meta.AgentSessionID == "" && meta.Prompt == "" {
		return domain.Session{}, fmt.Errorf("restore %s: nothing to resume from", id)
	}

	ws, err := m.workspace.Restore(ctx, ports.WorkspaceConfig{ProjectID: rec.ProjectID, SessionID: id, Branch: meta.Branch})
	if err != nil {
		return domain.Session{}, fmt.Errorf("restore %s: workspace: %w", id, err)
	}
	agentCfg := ports.AgentConfig{SessionID: id, WorkspacePath: ws.Path, Prompt: meta.Prompt}
	launch := m.agent.GetRestoreCommand(meta.AgentSessionID)
	if meta.AgentSessionID == "" {
		launch = m.agent.GetLaunchCommand(agentCfg)
	}
	handle, err := m.runtime.Create(ctx, ports.RuntimeConfig{
		SessionID:     id,
		WorkspacePath: ws.Path,
		LaunchCommand: launch,
		Env:           spawnEnv(m.agent.GetEnvironment(agentCfg), id, rec.ProjectID, rec.IssueID),
	})
	if err != nil {
		return domain.Session{}, fmt.Errorf("restore %s: runtime: %w", id, err)
	}
	outcome := ports.SpawnOutcome{Branch: ws.Branch, WorkspacePath: ws.Path, RuntimeHandle: handle, AgentSessionID: meta.AgentSessionID, Prompt: meta.Prompt}
	if err := m.lcm.OnSpawnCompleted(ctx, id, outcome); err != nil {
		_ = m.runtime.Destroy(ctx, handle)
		return domain.Session{}, fmt.Errorf("restore %s: completed: %w", id, err)
	}
	return m.Get(ctx, id)
}

// List returns the project's sessions as enriched display models.
func (m *Manager) List(ctx context.Context, project domain.ProjectID) ([]domain.Session, error) {
	recs, err := m.store.ListSessions(ctx, project)
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", project, err)
	}
	out := make([]domain.Session, 0, len(recs))
	for _, rec := range recs {
		s, err := m.toSession(ctx, rec)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

// Get returns one session as a display model, or ErrNotFound if it is absent.
func (m *Manager) Get(ctx context.Context, id domain.SessionID) (domain.Session, error) {
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return domain.Session{}, fmt.Errorf("get %s: %w", id, err)
	}
	if !ok {
		return domain.Session{}, fmt.Errorf("get %s: %w", id, ErrNotFound)
	}
	return m.toSession(ctx, rec)
}

// Send delivers a message to a running session's agent via the messenger.
func (m *Manager) Send(ctx context.Context, id domain.SessionID, message string) error {
	if err := m.messenger.Send(ctx, id, message); err != nil {
		return fmt.Errorf("send %s: %w", id, err)
	}
	return nil
}

// Cleanup reclaims the workspaces of terminal sessions in a project. A workspace
// whose teardown is refused (uncommitted work) is skipped, never forced.
func (m *Manager) Cleanup(ctx context.Context, project domain.ProjectID) ([]domain.SessionID, error) {
	recs, err := m.store.ListSessions(ctx, project)
	if err != nil {
		return nil, fmt.Errorf("cleanup %s: %w", project, err)
	}
	var cleaned []domain.SessionID
	for _, rec := range recs {
		if !isTerminal(rec.Lifecycle.Session.State) {
			continue
		}
		ws := workspaceInfo(rec)
		if ws.Path == "" {
			continue
		}
		if h := runtimeHandle(rec.Metadata); h.ID != "" {
			_ = m.runtime.Destroy(ctx, h) // best effort; usually already gone
		}
		if err := m.workspace.Destroy(ctx, ws); err != nil {
			continue // skipped: uncommitted work
		}
		cleaned = append(cleaned, rec.ID)
	}
	return cleaned, nil
}

// ---- helpers ----

func (m *Manager) toSession(ctx context.Context, rec domain.SessionRecord) (domain.Session, error) {
	pr, err := m.store.PRFactsForSession(ctx, rec.ID)
	if err != nil {
		return domain.Session{}, fmt.Errorf("pr facts %s: %w", rec.ID, err)
	}
	return domain.Session{SessionRecord: rec, Status: domain.DeriveStatus(rec.Lifecycle, pr)}, nil
}

func isTerminal(s domain.SessionState) bool {
	return s == domain.SessionDone || s == domain.SessionTerminated
}

func seedRecord(cfg ports.SpawnConfig, now time.Time) domain.SessionRecord {
	return domain.SessionRecord{
		ProjectID: cfg.ProjectID,
		IssueID:   cfg.IssueID,
		Kind:      cfg.Kind,
		CreatedAt: now,
		UpdatedAt: now,
		Lifecycle: domain.CanonicalSessionLifecycle{
			Version: domain.LifecycleVersion,
			Session: domain.SessionSubstate{State: domain.SessionNotStarted},
			Harness: cfg.Harness,
		},
	}
}

// buildPrompt assembles the spawn prompt from the explicit config (the full
// 3-layer assembly lands later).
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

func runtimeHandle(meta domain.SessionMetadata) ports.RuntimeHandle {
	return ports.RuntimeHandle{ID: meta.RuntimeHandleID, RuntimeName: meta.RuntimeName}
}

func workspaceInfo(rec domain.SessionRecord) ports.WorkspaceInfo {
	return ports.WorkspaceInfo{
		Path:      rec.Metadata.WorkspacePath,
		Branch:    rec.Metadata.Branch,
		SessionID: rec.ID,
		ProjectID: rec.ProjectID,
	}
}
