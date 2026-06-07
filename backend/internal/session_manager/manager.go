// Package sessionmanager drives internal session command operations over runtime,
// agent, workspace, storage, messenger, and lifecycle dependencies.
package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Sentinel errors returned by the Session Manager; callers match them with
// errors.Is.
var (
	ErrNotFound         = errors.New("session: not found")
	ErrNotRestorable    = errors.New("session: not restorable (not terminal)")
	ErrTerminated       = errors.New("session: terminated")
	ErrIncompleteHandle = errors.New("session: incomplete teardown handle")
	// ErrProjectNotResolvable means the spawn's project has no usable repo
	// (unregistered, archived, or missing a path). The API maps it to a 400.
	ErrProjectNotResolvable = errors.New("session: project repo not resolvable")
)

// Env vars a spawned process reads to learn who it is.
const (
	EnvSessionID = "AO_SESSION_ID"
	EnvProjectID = "AO_PROJECT_ID"
	EnvIssueID   = "AO_ISSUE_ID"
	// EnvDataDir tells a spawned agent's AO hook commands where the store lives.
	EnvDataDir = "AO_DATA_DIR"
)

type lifecycleRecorder interface {
	MarkSpawned(ctx context.Context, id domain.SessionID, metadata domain.SessionMetadata) error
	MarkTerminated(ctx context.Context, id domain.SessionID) error
}

type runtimeController interface {
	Create(ctx context.Context, cfg ports.RuntimeConfig) (ports.RuntimeHandle, error)
	Destroy(ctx context.Context, handle ports.RuntimeHandle) error
}

// Store is the persistence surface needed by the internal session Manager.
type Store interface {
	CreateSession(ctx context.Context, rec domain.SessionRecord) (domain.SessionRecord, error)
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
	ListSessions(ctx context.Context, project domain.ProjectID) ([]domain.SessionRecord, error)
	ListAllSessions(ctx context.Context) ([]domain.SessionRecord, error)
	// DeleteSession removes a session row only if it is still in seed state
	// (no workspace, runtime handle, agent session id, or prompt; not
	// terminated). Returns deleted=true when removal happened; deleted=false
	// when the row had already progressed past seed state — preserving the
	// no-resurrection guarantee for live sessions.
	DeleteSession(ctx context.Context, id domain.SessionID) (bool, error)
}

// Manager coordinates internal session spawn, restore, kill, and cleanup over
// the outbound ports. User-facing read-model assembly lives in the service package.
type Manager struct {
	runtime   runtimeController
	agents    ports.AgentResolver
	workspace ports.Workspace
	store     Store
	messenger ports.AgentMessenger
	lcm       lifecycleRecorder
	dataDir   string
	clock     func() time.Time
	// lookPath is exec.LookPath in production; tests substitute a stub so
	// they don't need real binaries on PATH. Returns ports.ErrAgentBinaryNotFound
	// when the binary is missing so the sentinel propagates through toAPIError.
	lookPath func(string) (string, error)
}

// Deps are the collaborators a Session Manager needs; New wires them together.
type Deps struct {
	Runtime   runtimeController
	Agents    ports.AgentResolver
	Workspace ports.Workspace
	Store     Store
	Messenger ports.AgentMessenger
	Lifecycle lifecycleRecorder
	// DataDir is exported to spawned agents as AO_DATA_DIR so their hook
	// commands can open the same store.
	DataDir string
	Clock   func() time.Time
	// LookPath overrides exec.LookPath for the pre-launch agent-binary check.
	// Production wiring leaves this nil and the manager defaults to
	// exec.LookPath; tests inject a stub so they need not seed real binaries.
	LookPath func(string) (string, error)
}

// New builds a Session Manager from its dependencies, defaulting the clock to
// time.Now when Deps.Clock is nil.
func New(d Deps) *Manager {
	m := &Manager{
		runtime:   d.Runtime,
		agents:    d.Agents,
		workspace: d.Workspace,
		store:     d.Store,
		messenger: d.Messenger,
		lcm:       d.Lifecycle,
		dataDir:   d.DataDir,
		clock:     d.Clock,
		lookPath:  d.LookPath,
	}
	if m.clock == nil {
		m.clock = time.Now
	}
	if m.lookPath == nil {
		m.lookPath = exec.LookPath
	}
	return m
}

// Spawn creates the session row (which assigns the "{project}-{n}" id), then the
// workspace and runtime, then reports completion to the LCM. A failure after the
// row exists parks it as terminated and rolls back what was built.
func (m *Manager) Spawn(ctx context.Context, cfg ports.SpawnConfig) (domain.SessionRecord, error) {
	prompt, err := m.buildSpawnPrompt(ctx, cfg)
	if err != nil {
		return domain.SessionRecord{}, fmt.Errorf("spawn: prompt: %w", err)
	}

	rec, err := m.store.CreateSession(ctx, seedRecord(cfg, m.clock()))
	if err != nil {
		return domain.SessionRecord{}, fmt.Errorf("spawn: create: %w", err)
	}
	id := rec.ID

	branch := cfg.Branch
	if branch == "" {
		// A fresh, unique branch per session: gitworktree can't add a worktree on
		// a branch already checked out elsewhere (e.g. main), so default to one
		// derived from the assigned session id.
		branch = "ao/" + string(id)
	}
	ws, err := m.workspace.Create(ctx, ports.WorkspaceConfig{ProjectID: cfg.ProjectID, SessionID: id, Branch: branch})
	if err != nil {
		m.markSpawnFailedTerminated(ctx, id)
		return domain.SessionRecord{}, fmt.Errorf("spawn %s: workspace: %w", id, err)
	}

	agent, ok := m.agents.Agent(cfg.Harness)
	if !ok {
		_ = m.workspace.Destroy(ctx, ws)
		m.markSpawnFailedTerminated(ctx, id)
		return domain.SessionRecord{}, fmt.Errorf("spawn %s: no agent adapter for harness %q", id, cfg.Harness)
	}
	if err := m.prepareWorkspace(ctx, agent, id, ws.Path); err != nil {
		_ = m.workspace.Destroy(ctx, ws)
		m.markSpawnFailedTerminated(ctx, id)
		return domain.SessionRecord{}, fmt.Errorf("spawn %s: %w", id, err)
	}
	argv, err := agent.GetLaunchCommand(ctx, ports.LaunchConfig{
		SessionID:     string(id),
		WorkspacePath: ws.Path,
		Prompt:        prompt,
		IssueID:       string(cfg.IssueID),
	})
	if err != nil {
		_ = m.workspace.Destroy(ctx, ws)
		m.markSpawnFailedTerminated(ctx, id)
		return domain.SessionRecord{}, fmt.Errorf("spawn %s: launch command: %w", id, err)
	}
	// Pre-flight: confirm argv[0] actually exists on PATH (or as an absolute
	// path the adapter returned) BEFORE handing the launch to the runtime.
	// Zellij happily creates a session+pane around a missing command, so an
	// unresolved binary would leak through as a "live" session that never ran.
	if err := m.validateAgentBinary(argv); err != nil {
		_ = m.workspace.Destroy(ctx, ws)
		m.markSpawnFailedTerminated(ctx, id)
		return domain.SessionRecord{}, fmt.Errorf("spawn %s: %w", id, err)
	}
	handle, err := m.runtime.Create(ctx, ports.RuntimeConfig{
		SessionID:     id,
		WorkspacePath: ws.Path,
		Argv:          argv,
		Env:           spawnEnv(id, cfg.ProjectID, cfg.IssueID, m.dataDir),
	})
	if err != nil {
		_ = m.workspace.Destroy(ctx, ws)
		m.markSpawnFailedTerminated(ctx, id)
		return domain.SessionRecord{}, fmt.Errorf("spawn %s: runtime: %w", id, err)
	}

	metadata := domain.SessionMetadata{Branch: ws.Branch, WorkspacePath: ws.Path, RuntimeHandleID: handle.ID, Prompt: prompt}
	if err := m.lcm.MarkSpawned(ctx, id, metadata); err != nil {
		_ = m.runtime.Destroy(ctx, handle)
		_ = m.workspace.Destroy(ctx, ws)
		m.markSpawnFailedTerminated(ctx, id)
		return domain.SessionRecord{}, fmt.Errorf("spawn %s: completed: %w", id, err)
	}
	return m.getRecord(ctx, id)
}

// markSpawnFailedTerminated best-effort parks an orphaned spawn as terminated.
// A phantom half-spawned row is worse than a terminal one; we only delete the
// row when nothing observable has landed yet (seed state) via rollbackSpawn.
func (m *Manager) markSpawnFailedTerminated(ctx context.Context, id domain.SessionID) {
	_ = m.lcm.MarkTerminated(ctx, id)
}

// rollbackSpawn deletes a session row when it is still in seed state — used
// when an out-of-band step that happens AFTER `Spawn` returns (e.g. PR claim
// over HTTP) has failed and the caller wants the partially-spawned session
// gone without leaving a terminated orphan visible under `--include-terminated`.
//
// If the row has progressed past seed state (workspace exists, runtime created,
// etc.), DeleteSession is a no-op and rollbackSpawn falls back to a Kill so the
// runtime/workspace are torn down. Returns (deleted, killed):
//   - deleted=true: the row was a seed row and has been removed
//   - killed=true:  the row had spawn output and was torn down + terminated
//   - both false:   the row was already terminated or absent — benign no-op
func (m *Manager) rollbackSpawn(ctx context.Context, id domain.SessionID) (deleted, killed bool, err error) {
	deleted, err = m.store.DeleteSession(ctx, id)
	if err != nil {
		return false, false, fmt.Errorf("rollback %s: %w", id, err)
	}
	if deleted {
		return true, false, nil
	}
	killed, err = m.Kill(ctx, id)
	if err != nil {
		return false, false, err
	}
	return false, killed, nil
}

// RollbackSpawn is the public surface of rollbackSpawn for service-layer callers.
func (m *Manager) RollbackSpawn(ctx context.Context, id domain.SessionID) (deleted, killed bool, err error) {
	return m.rollbackSpawn(ctx, id)
}

// Kill records terminal intent with the LCM, then tears down the runtime and
// workspace. A workspace teardown refused by the worktree-remove safety
// (uncommitted work) surfaces as an error with freed=false and is never forced.
func (m *Manager) Kill(ctx context.Context, id domain.SessionID) (bool, error) {
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
	if err := m.lcm.MarkTerminated(ctx, id); err != nil {
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
// before any durable session write, so a failure never resurrects the row or destroys
// the worktree (it may hold the agent's prior work).
func (m *Manager) Restore(ctx context.Context, id domain.SessionID) (domain.SessionRecord, error) {
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return domain.SessionRecord{}, fmt.Errorf("restore %s: %w", id, err)
	}
	if !ok {
		return domain.SessionRecord{}, fmt.Errorf("restore %s: %w", id, ErrNotFound)
	}
	if !rec.IsTerminated {
		return domain.SessionRecord{}, fmt.Errorf("restore %s: %w", id, ErrNotRestorable)
	}
	meta := rec.Metadata
	// Mirror Kill's incomplete-handle guard: a session whose spawn failed before
	// the workspace landed has neither WorkspacePath nor Branch, and there is
	// nothing meaningful to restore from. Surface this as a typed 409 instead of
	// letting workspace.Restore fail with an opaque wrapped error.
	if meta.WorkspacePath == "" || meta.Branch == "" {
		return domain.SessionRecord{}, fmt.Errorf("restore %s: %w", id, ErrIncompleteHandle)
	}
	if meta.AgentSessionID == "" && meta.Prompt == "" {
		return domain.SessionRecord{}, fmt.Errorf("restore %s: nothing to resume from", id)
	}

	ws, err := m.workspace.Restore(ctx, ports.WorkspaceConfig{ProjectID: rec.ProjectID, SessionID: id, Branch: meta.Branch})
	if err != nil {
		return domain.SessionRecord{}, fmt.Errorf("restore %s: workspace: %w", id, err)
	}
	agent, ok := m.agents.Agent(rec.Harness)
	if !ok {
		return domain.SessionRecord{}, fmt.Errorf("restore %s: no agent adapter for harness %q", id, rec.Harness)
	}
	if err := m.prepareWorkspace(ctx, agent, id, ws.Path); err != nil {
		return domain.SessionRecord{}, fmt.Errorf("restore %s: %w", id, err)
	}
	argv, err := restoreArgv(ctx, agent, id, ws.Path, meta)
	if err != nil {
		return domain.SessionRecord{}, fmt.Errorf("restore %s: %w", id, err)
	}
	handle, err := m.runtime.Create(ctx, ports.RuntimeConfig{
		SessionID:     id,
		WorkspacePath: ws.Path,
		Argv:          argv,
		Env:           spawnEnv(id, rec.ProjectID, rec.IssueID, m.dataDir),
	})
	if err != nil {
		return domain.SessionRecord{}, fmt.Errorf("restore %s: runtime: %w", id, err)
	}
	metadata := domain.SessionMetadata{Branch: ws.Branch, WorkspacePath: ws.Path, RuntimeHandleID: handle.ID, AgentSessionID: meta.AgentSessionID, Prompt: meta.Prompt}
	if err := m.lcm.MarkSpawned(ctx, id, metadata); err != nil {
		_ = m.runtime.Destroy(ctx, handle)
		return domain.SessionRecord{}, fmt.Errorf("restore %s: completed: %w", id, err)
	}
	return m.getRecord(ctx, id)
}

func (m *Manager) getRecord(ctx context.Context, id domain.SessionID) (domain.SessionRecord, error) {
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return domain.SessionRecord{}, fmt.Errorf("get %s: %w", id, err)
	}
	if !ok {
		return domain.SessionRecord{}, fmt.Errorf("get %s: %w", id, ErrNotFound)
	}
	return rec, nil
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
	recs, err := m.cleanupRecords(ctx, project)
	if err != nil {
		return nil, fmt.Errorf("cleanup %s: %w", project, err)
	}
	cleaned := make([]domain.SessionID, 0, len(recs))
	for _, rec := range recs {
		if !rec.IsTerminated {
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

func (m *Manager) cleanupRecords(ctx context.Context, project domain.ProjectID) ([]domain.SessionRecord, error) {
	if project == "" {
		return m.store.ListAllSessions(ctx)
	}
	return m.store.ListSessions(ctx, project)
}

// ---- helpers ----

func seedRecord(cfg ports.SpawnConfig, now time.Time) domain.SessionRecord {
	return domain.SessionRecord{
		ProjectID: cfg.ProjectID,
		IssueID:   cfg.IssueID,
		Kind:      cfg.Kind,
		CreatedAt: now,
		UpdatedAt: now,
		Harness:   cfg.Harness,
		Activity:  domain.Activity{State: domain.ActivityIdle, LastActivityAt: now},
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

func (m *Manager) buildSpawnPrompt(ctx context.Context, cfg ports.SpawnConfig) (string, error) {
	prompt := buildPrompt(cfg)
	switch cfg.Kind {
	case domain.KindOrchestrator:
		return appendPromptSection(orchestratorPrompt(cfg.ProjectID), prompt), nil
	case domain.KindWorker:
		orchestratorID, ok, err := m.activeOrchestratorSessionID(ctx, cfg.ProjectID)
		if err != nil {
			return "", err
		}
		if ok {
			prompt = appendPromptSection(prompt, workerOrchestratorPrompt(orchestratorID))
		}
	}
	return prompt, nil
}

func (m *Manager) activeOrchestratorSessionID(ctx context.Context, project domain.ProjectID) (domain.SessionID, bool, error) {
	recs, err := m.store.ListSessions(ctx, project)
	if err != nil {
		return "", false, fmt.Errorf("list sessions for %s: %w", project, err)
	}
	for _, rec := range recs {
		if rec.Kind == domain.KindOrchestrator && !rec.IsTerminated {
			return rec.ID, true, nil
		}
	}
	return "", false, nil
}

func orchestratorPrompt(project domain.ProjectID) string {
	return fmt.Sprintf(`## Orchestrator role

You are the human-facing coordinator for project %s. Coordinate work for the human, keep the project moving, and avoid doing implementation yourself unless it is necessary.

Spawn worker sessions for implementation with:
`+"`ao spawn --project %s --prompt \"<clear worker task>\"`"+`

Message workers with `+"`ao send`"+`, for example:
`+"`ao send --session <worker-session-id> --message \"<your message>\"`"+`

Use workers for focused implementation tasks, track their progress, synthesize their results, and only step into implementation directly for true emergencies or small coordination fixes.`, project, project)
}

func workerOrchestratorPrompt(orchestratorID domain.SessionID) string {
	return fmt.Sprintf(`## Orchestrator coordination

An active orchestrator session exists for this project. If you hit a true blocker or need cross-session coordination, message it with:
`+"`ao send --session %s --message \"<your message>\"`"+`

Only ping the orchestrator for true blockers, cross-session coordination, or decisions that cannot be resolved within your own task.`, orchestratorID)
}

func appendPromptSection(prompt, section string) string {
	switch {
	case prompt == "":
		return section
	case section == "":
		return prompt
	default:
		return prompt + "\n\n" + section
	}
}

func spawnEnv(id domain.SessionID, project domain.ProjectID, issue domain.IssueID, dataDir string) map[string]string {
	return map[string]string{
		EnvSessionID: string(id),
		EnvProjectID: string(project),
		EnvIssueID:   string(issue),
		EnvDataDir:   dataDir,
	}
}

// preLauncher is an optional Agent capability: a step the manager runs before
// launch. Claude Code implements it to record workspace trust in ~/.claude.json
// so its interactive "do you trust this folder?" dialog can't block the headless
// pane. Adapters that don't need it simply omit the method.
type preLauncher interface {
	PreLaunch(ctx context.Context, cfg ports.LaunchConfig) error
}

// prepareWorkspace runs the per-session pre-launch steps before the runtime
// starts the agent: installing the workspace-local activity hooks (so early
// startup hooks can update the already-created session row), then any optional
// PreLaunch step. Shared by Spawn and Restore.
func (m *Manager) prepareWorkspace(ctx context.Context, agent ports.Agent, id domain.SessionID, workspacePath string) error {
	if err := agent.GetAgentHooks(ctx, ports.WorkspaceHookConfig{
		SessionID:     string(id),
		WorkspacePath: workspacePath,
		DataDir:       m.dataDir,
	}); err != nil {
		return fmt.Errorf("install hooks: %w", err)
	}
	if pl, ok := agent.(preLauncher); ok {
		if err := pl.PreLaunch(ctx, ports.LaunchConfig{SessionID: string(id), WorkspacePath: workspacePath}); err != nil {
			return fmt.Errorf("pre-launch: %w", err)
		}
	}
	return nil
}

// restoreArgv builds the argv to relaunch a torn-down session: the agent's
// native resume command when it can continue the session, else a fresh launch.
// The agent signals via ok=false (e.g. no native session id captured yet).
func restoreArgv(ctx context.Context, agent ports.Agent, id domain.SessionID, workspacePath string, meta domain.SessionMetadata) ([]string, error) {
	ref := ports.SessionRef{
		ID:            string(id),
		WorkspacePath: workspacePath,
		Metadata:      map[string]string{ports.MetadataKeyAgentSessionID: meta.AgentSessionID},
	}
	cmd, ok, err := agent.GetRestoreCommand(ctx, ports.RestoreConfig{Session: ref})
	if err != nil {
		return nil, fmt.Errorf("restore command: %w", err)
	}
	if ok {
		return cmd, nil
	}
	argv, err := agent.GetLaunchCommand(ctx, ports.LaunchConfig{
		SessionID:     string(id),
		WorkspacePath: workspacePath,
		Prompt:        meta.Prompt,
	})
	if err != nil {
		return nil, fmt.Errorf("launch command: %w", err)
	}
	return argv, nil
}

// validateAgentBinary checks that argv[0] resolves via the manager's
// lookPath (exec.LookPath in prod) before any runtime work happens. Adapters
// that can't resolve their binary now return ports.ErrAgentBinaryNotFound from
// GetLaunchCommand directly; this guard is a defense-in-depth for adapters
// that return an argv[0] like "claude" without verifying.
func (m *Manager) validateAgentBinary(argv []string) error {
	if len(argv) == 0 {
		return fmt.Errorf("agent: empty launch argv: %w", ports.ErrAgentBinaryNotFound)
	}
	bin := argv[0]
	if _, err := m.lookPath(bin); err != nil {
		return fmt.Errorf("agent binary %q: %w", bin, ports.ErrAgentBinaryNotFound)
	}
	return nil
}

func runtimeHandle(meta domain.SessionMetadata) ports.RuntimeHandle {
	return ports.RuntimeHandle{ID: meta.RuntimeHandleID}
}

func workspaceInfo(rec domain.SessionRecord) ports.WorkspaceInfo {
	return ports.WorkspaceInfo{
		Path:      rec.Metadata.WorkspacePath,
		Branch:    rec.Metadata.Branch,
		SessionID: rec.ID,
		ProjectID: rec.ProjectID,
	}
}
