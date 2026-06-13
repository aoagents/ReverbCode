// Package sessionmanager drives internal session command operations over runtime,
// agent, workspace, storage, messenger, and lifecycle dependencies.
package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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

// hookBinaryName is the executable name the workspace hook commands invoke:
// every agent adapter installs a bare `ao hooks <agent> <event>`. The session
// PATH pin (hookPATH) only works when the daemon's own executable carries this
// name, since prepending its directory must change what `ao` resolves to.
const hookBinaryName = "ao"

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
	// GetProject loads a project row so spawn can resolve its per-project agent
	// config into the launch command. ok=false means the project is unknown.
	GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error)
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
	// executable resolves the daemon's own binary (os.Executable in
	// production); its directory is prepended to spawned sessions' PATH so the
	// workspace hook commands resolve back to this daemon. Tests inject a stub.
	executable func() (string, error)
	logger     *slog.Logger
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
	// Executable overrides os.Executable for the session PATH pin (see
	// hookPATH). Production wiring leaves this nil; tests inject a stub so they
	// control what the test binary appears to be.
	Executable func() (string, error)
	// Logger receives spawn-time diagnostics (e.g. when the session PATH
	// cannot be pinned to the daemon binary). Nil defaults to slog.Default().
	Logger *slog.Logger
}

// New builds a Session Manager from its dependencies, defaulting the clock to
// time.Now when Deps.Clock is nil.
func New(d Deps) *Manager {
	m := &Manager{
		runtime:    d.Runtime,
		agents:     d.Agents,
		workspace:  d.Workspace,
		store:      d.Store,
		messenger:  d.Messenger,
		lcm:        d.Lifecycle,
		dataDir:    d.DataDir,
		clock:      d.Clock,
		lookPath:   d.LookPath,
		executable: d.Executable,
		logger:     d.Logger,
	}
	if m.clock == nil {
		m.clock = time.Now
	}
	if m.lookPath == nil {
		m.lookPath = exec.LookPath
	}
	if m.executable == nil {
		m.executable = os.Executable
	}
	if m.logger == nil {
		m.logger = slog.Default()
	}
	return m
}

// Spawn creates the session row (which assigns the "{project}-{n}" id), then the
// workspace and runtime, then reports completion to the LCM. If workspace
// materialization fails the still-seed row is deleted outright; a later failure
// parks the row as terminated and rolls back what was built.
func (m *Manager) Spawn(ctx context.Context, cfg ports.SpawnConfig) (domain.SessionRecord, error) {
	project, err := m.loadProject(ctx, cfg.ProjectID)
	if err != nil {
		return domain.SessionRecord{}, fmt.Errorf("spawn: %w", err)
	}
	// A per-project role override picks the harness when the spawn names none,
	// so a project can default workers to one agent and orchestrators to another.
	cfg.Harness = effectiveHarness(cfg.Harness, cfg.Kind, project.Config)

	prompt, systemPrompt, err := m.buildSpawnTexts(ctx, cfg)
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
		branch = defaultSessionBranch(id, cfg.Kind, sessionPrefix(project))
	}
	ws, err := m.workspace.Create(ctx, ports.WorkspaceConfig{
		ProjectID:     cfg.ProjectID,
		SessionID:     id,
		Kind:          cfg.Kind,
		SessionPrefix: sessionPrefix(project),
		Branch:        branch,
		BaseBranch:    project.Config.WithDefaults().DefaultBranch,
	})
	if err != nil {
		// Nothing observable exists yet — no worktree, no runtime — so the seed
		// row is deleted outright instead of accumulating as a terminated orphan
		// in session lists (e.g. when gitworktree refuses the branch).
		m.rollbackSpawnSeedRow(ctx, id)
		return domain.SessionRecord{}, fmt.Errorf("spawn %s: workspace: %w", id, err)
	}

	// Per-project workspace provisioning: symlink shared files, then run any
	// post-create commands (e.g. `pnpm install`) before the agent launches.
	if err := m.provisionWorkspace(ctx, project, ws.Path); err != nil {
		_ = m.workspace.Destroy(ctx, ws)
		m.markSpawnFailedTerminated(ctx, id)
		return domain.SessionRecord{}, fmt.Errorf("spawn %s: provision: %w", id, err)
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
	agentConfig := effectiveAgentConfig(cfg.Kind, project.Config)
	argv, err := agent.GetLaunchCommand(ctx, ports.LaunchConfig{
		SessionID:     string(id),
		WorkspacePath: ws.Path,
		Prompt:        prompt,
		SystemPrompt:  systemPrompt,
		IssueID:       string(cfg.IssueID),
		Config:        agentConfig,
		Permissions:   agentConfig.Permissions,
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
		Env:           m.runtimeEnv(id, cfg.ProjectID, cfg.IssueID, project.Config.Env),
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

// loadProject loads the project record so spawn can resolve its per-project
// config (harness/agent overrides, env, branch, rules, provisioning). A missing
// project yields a zero record rather than an error: the project may be
// unregistered yet still have live sessions, and an empty config simply means
// every field falls back to its default.
func (m *Manager) loadProject(ctx context.Context, projectID domain.ProjectID) (domain.ProjectRecord, error) {
	row, ok, err := m.store.GetProject(ctx, string(projectID))
	if err != nil {
		return domain.ProjectRecord{}, fmt.Errorf("load project: %w", err)
	}
	if !ok {
		return domain.ProjectRecord{}, nil
	}
	return row, nil
}

// effectiveHarness resolves the harness for a spawn: an explicit harness wins;
// otherwise the project's role override for the session kind applies; otherwise
// it stays empty so the daemon's global default (AO_AGENT) is used downstream.
func effectiveHarness(explicit domain.AgentHarness, kind domain.SessionKind, cfg domain.ProjectConfig) domain.AgentHarness {
	if explicit != "" {
		return explicit
	}
	if role := roleOverride(kind, cfg).Harness; role != "" {
		return role
	}
	return ""
}

// effectiveAgentConfig merges the role override's agent config over the
// project's base agent config; set override fields win.
func effectiveAgentConfig(kind domain.SessionKind, cfg domain.ProjectConfig) ports.AgentConfig {
	merged := cfg.AgentConfig
	override := roleOverride(kind, cfg).AgentConfig
	if override.Model != "" {
		merged.Model = override.Model
	}
	if override.Permissions != "" {
		merged.Permissions = override.Permissions
	}
	return merged
}

func roleOverride(kind domain.SessionKind, cfg domain.ProjectConfig) domain.RoleOverride {
	if kind == domain.KindOrchestrator {
		return cfg.Orchestrator
	}
	return cfg.Worker
}

// sessionPrefix returns the display prefix for a project: the explicit
// SessionPrefix when set, otherwise the first 12 characters of the project ID.
func sessionPrefix(project domain.ProjectRecord) string {
	if p := strings.TrimSpace(project.Config.SessionPrefix); p != "" {
		return p
	}
	if len(project.ID) <= 12 {
		return project.ID
	}
	return project.ID[:12]
}

// markSpawnFailedTerminated best-effort parks an orphaned spawn as terminated.
// A phantom half-spawned row is worse than a terminal one; we only delete the
// row when nothing observable has landed yet (seed state) via rollbackSpawn or
// rollbackSpawnSeedRow.
func (m *Manager) markSpawnFailedTerminated(ctx context.Context, id domain.SessionID) {
	_ = m.lcm.MarkTerminated(ctx, id)
}

// rollbackSpawnSeedRow best-effort removes the row of a spawn that failed
// before anything observable (worktree, runtime) was built, so failed spawns
// don't accumulate terminated rows in session lists. DeleteSession only removes
// rows still in seed state; if the row has progressed or the delete itself
// fails, fall back to parking it terminated so a phantom row never looks live.
// (Kill is not a usable fallback here: it refuses seed rows with
// ErrIncompleteHandle before recording terminal intent.)
func (m *Manager) rollbackSpawnSeedRow(ctx context.Context, id domain.SessionID) {
	if deleted, err := m.store.DeleteSession(ctx, id); err == nil && deleted {
		return
	}
	m.markSpawnFailedTerminated(ctx, id)
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
// (uncommitted work) is never forced: the session still terminates and Kill
// succeeds with freed=false, signalling the workspace was preserved.
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
		if errors.Is(err, ports.ErrWorkspaceDirty) {
			return false, nil
		}
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

	project, err := m.loadProject(ctx, rec.ProjectID)
	if err != nil {
		return domain.SessionRecord{}, fmt.Errorf("restore %s: %w", id, err)
	}
	ws, err := m.workspace.Restore(ctx, ports.WorkspaceConfig{
		ProjectID:     rec.ProjectID,
		SessionID:     id,
		Kind:          rec.Kind,
		SessionPrefix: sessionPrefix(project),
		Branch:        meta.Branch,
	})
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
	// The system prompt is derived, not persisted: recompute it so a restored
	// session keeps its standing instructions across the relaunch.
	systemPrompt, err := m.buildSystemPrompt(ctx, rec.Kind, rec.ProjectID)
	if err != nil {
		return domain.SessionRecord{}, fmt.Errorf("restore %s: system prompt: %w", id, err)
	}
	// Restore re-applies the project's resolved agent config so a configured
	// model/permissions carry across a restore, matching fresh spawn.
	argv, err := restoreArgv(ctx, agent, id, ws.Path, meta, systemPrompt, effectiveAgentConfig(rec.Kind, project.Config))
	if err != nil {
		return domain.SessionRecord{}, fmt.Errorf("restore %s: %w", id, err)
	}
	handle, err := m.runtime.Create(ctx, ports.RuntimeConfig{
		SessionID:     id,
		WorkspacePath: ws.Path,
		Argv:          argv,
		Env:           m.runtimeEnv(id, rec.ProjectID, rec.IssueID, project.Config.Env),
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

// CleanupSkip reports one terminal session whose workspace was preserved
// rather than reclaimed, and why.
type CleanupSkip struct {
	SessionID domain.SessionID
	Reason    string
}

// CleanupResult reports what Cleanup reclaimed and what it preserved.
type CleanupResult struct {
	Cleaned []domain.SessionID
	Skipped []CleanupSkip
}

// Cleanup reclaims the workspaces of terminal sessions in a project. A workspace
// whose teardown is refused (uncommitted work) is never forced; it is reported
// in Skipped with the reason so the refusal is visible instead of silent.
func (m *Manager) Cleanup(ctx context.Context, project domain.ProjectID) (CleanupResult, error) {
	recs, err := m.cleanupRecords(ctx, project)
	if err != nil {
		return CleanupResult{}, fmt.Errorf("cleanup %s: %w", project, err)
	}
	result := CleanupResult{Cleaned: make([]domain.SessionID, 0, len(recs)), Skipped: []CleanupSkip{}}
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
			if !errors.Is(err, ports.ErrWorkspaceDirty) {
				// The public reason stays a fixed string (the raw error carries
				// internal filesystem paths); the full cause lands here.
				m.logger.Warn("cleanup: workspace teardown failed", "sessionID", rec.ID, "path", ws.Path, "error", err)
			}
			result.Skipped = append(result.Skipped, CleanupSkip{SessionID: rec.ID, Reason: cleanupSkipReason(err)})
			continue
		}
		result.Cleaned = append(result.Cleaned, rec.ID)
	}
	return result, nil
}

// cleanupSkipReason renders a workspace teardown refusal as a short
// user-facing reason for the cleanup report. Deliberately not the raw error:
// it flows to the API response and CLI output, and teardown errors embed
// internal filesystem paths.
func cleanupSkipReason(err error) string {
	if errors.Is(err, ports.ErrWorkspaceDirty) {
		return "workspace has uncommitted changes"
	}
	return "workspace teardown failed"
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

func defaultSessionBranch(id domain.SessionID, kind domain.SessionKind, prefix string) string {
	if kind == domain.KindOrchestrator {
		return "ao/" + prefix + "-orchestrator"
	}
	// A fresh, unique branch per worker session: gitworktree can't add a worktree
	// on a branch already checked out elsewhere (e.g. main), so default to one
	// derived from the assigned session id.
	return "ao/" + string(id)
}

func buildPrompt(cfg ports.SpawnConfig) string {
	return cfg.Prompt
}

// buildSpawnTexts returns the user-facing prompt and the system prompt to
// deliver separately to the agent. Orchestrator role instructions and worker
// coordination hints are placed in the system prompt so they are treated as
// standing instructions rather than part of the human's task request. A
// promptless spawn delivers no user prompt at all: the agent simply lands at an
// empty input box rather than receiving an auto-generated kickoff turn.
func (m *Manager) buildSpawnTexts(ctx context.Context, cfg ports.SpawnConfig) (prompt, systemPrompt string, err error) {
	prompt = buildPrompt(cfg)
	systemPrompt, err = m.buildSystemPrompt(ctx, cfg.Kind, cfg.ProjectID)
	if err != nil {
		return "", "", err
	}
	return prompt, systemPrompt, nil
}

// buildSystemPrompt derives the standing instructions for a session of the
// given kind from current store state. Restore recomputes them through here
// rather than persisting them, so a restored worker points at the orchestrator
// that is active now, not the one from its original spawn.
func (m *Manager) buildSystemPrompt(ctx context.Context, kind domain.SessionKind, projectID domain.ProjectID) (string, error) {
	switch kind {
	case domain.KindOrchestrator:
		return orchestratorPrompt(projectID), nil
	case domain.KindWorker:
		orchestratorID, ok, err := m.activeOrchestratorSessionID(ctx, projectID)
		if err != nil {
			return "", err
		}
		if ok {
			return workerOrchestratorPrompt(orchestratorID), nil
		}
	}
	return "", nil
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

// spawnEnv builds the runtime environment: the per-project env vars first, then
// the AO-internal vars last so they always win (a project cannot override
// AO_SESSION_ID and friends).
func spawnEnv(id domain.SessionID, project domain.ProjectID, issue domain.IssueID, dataDir string, projectEnv map[string]string) map[string]string {
	env := make(map[string]string, len(projectEnv)+4)
	for k, v := range projectEnv {
		env[k] = v
	}
	env[EnvSessionID] = string(id)
	env[EnvProjectID] = string(project)
	env[EnvIssueID] = string(issue)
	env[EnvDataDir] = dataDir
	return env
}

// runtimeEnv is spawnEnv plus the hook PATH pin: the session's PATH puts the
// running daemon's own directory first, so the bare `ao` in workspace hook
// commands resolves to the daemon that installed them rather than whatever
// `ao` is first on the inherited PATH (e.g. a legacy CLI without the hooks
// command, which fails every callback and silently kills activity tracking).
// When the pin cannot be applied the inherited PATH is kept and a warning is
// logged so the degradation isn't silent.
func (m *Manager) runtimeEnv(id domain.SessionID, project domain.ProjectID, issue domain.IssueID, projectEnv map[string]string) map[string]string {
	env := spawnEnv(id, project, issue, m.dataDir, projectEnv)
	path, err := hookPATH(m.executable, os.Getenv, projectEnv)
	if err != nil {
		m.logger.Warn("session PATH not pinned to the daemon binary; `ao hooks` callbacks may resolve to a different ao and activity tracking will stall",
			"session", id, "error", err)
		return env
	}
	env["PATH"] = path
	return env
}

// hookPATH builds the PATH value pinned into a spawned session: the daemon
// executable's directory prepended to the base PATH (the project's PATH
// override when set, else the daemon's inherited PATH — matching what the
// runtime would have exported anyway). An error means the pin cannot be
// applied: the executable is unresolvable, or is not named "ao", in which case
// prepending its directory would not change what `ao` resolves to.
func hookPATH(executable func() (string, error), getenv func(string) string, projectEnv map[string]string) (string, error) {
	exe, err := executable()
	if err != nil {
		return "", fmt.Errorf("resolve daemon executable: %w", err)
	}
	name := filepath.Base(exe)
	if runtime.GOOS == "windows" {
		name = strings.TrimSuffix(strings.ToLower(name), ".exe")
	}
	if name != hookBinaryName {
		return "", fmt.Errorf("daemon executable %s is not named %q", exe, hookBinaryName)
	}
	base := projectEnv["PATH"]
	if base == "" {
		base = getenv("PATH")
	}
	dir := filepath.Dir(exe)
	if base == "" {
		return dir, nil
	}
	return dir + string(os.PathListSeparator) + base, nil
}

// provisionWorkspace applies the project's per-workspace setup after the
// worktree exists: symlink shared files from the project repo, then run any
// post-create commands. Either failing aborts the spawn so a half-provisioned
// workspace never launches an agent.
func (m *Manager) provisionWorkspace(ctx context.Context, project domain.ProjectRecord, workspacePath string) error {
	if err := applySymlinks(project.Path, workspacePath, project.Config.Symlinks); err != nil {
		return err
	}
	return runPostCreate(ctx, workspacePath, project.Config.PostCreate)
}

// applySymlinks links each repo-relative path into the workspace. A source that
// does not exist is skipped (symlinks are a convenience for optional files like
// .env); a real link failure aborts. Paths must be repo-relative with no
// parent traversal (no leading "/", no ".." segment) — a bad path is refused
// up front so a project config cannot escape the project or workspace tree.
func applySymlinks(projectPath, workspacePath string, symlinks []string) error {
	for _, rel := range symlinks {
		rel = strings.TrimSpace(rel)
		if rel == "" {
			continue
		}
		clean, err := safeRelPath(rel)
		if err != nil {
			return fmt.Errorf("symlink %q: %w", rel, err)
		}
		source := filepath.Join(projectPath, clean)
		if _, err := os.Stat(source); err != nil {
			continue
		}
		target := filepath.Join(workspacePath, clean)
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return fmt.Errorf("symlink %q: %w", rel, err)
		}
		if _, err := os.Lstat(target); err == nil {
			continue
		}
		if err := os.Symlink(source, target); err != nil {
			return fmt.Errorf("symlink %q: %w", rel, err)
		}
	}
	return nil
}

// safeRelPath confines rel to a repo-relative path: no absolute paths and no
// ".." segments (before or after Clean). The cleaned form is returned so
// callers join it against project/workspace roots safely.
func safeRelPath(rel string) (string, error) {
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, `\`) {
		return "", fmt.Errorf("path must be repo-relative")
	}
	clean := filepath.Clean(rel)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean == "." || clean == "" {
		return "", fmt.Errorf("path must be repo-relative")
	}
	for _, seg := range strings.Split(filepath.ToSlash(clean), "/") {
		if seg == ".." {
			return "", fmt.Errorf("path must be repo-relative")
		}
	}
	return clean, nil
}

// runPostCreate runs each post-create command in the workspace via the platform
// shell, so OS-agnostic commands like "pnpm install" work. A non-zero exit
// aborts the spawn with the command output.
func runPostCreate(ctx context.Context, workspacePath string, commands []string) error {
	for _, command := range commands {
		command = strings.TrimSpace(command)
		if command == "" {
			continue
		}
		var cmd *exec.Cmd
		if runtime.GOOS == "windows" {
			cmd = exec.CommandContext(ctx, "cmd", "/c", command)
		} else {
			cmd = exec.CommandContext(ctx, "sh", "-c", command)
		}
		cmd.Dir = workspacePath
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("postCreate %q: %w: %s", command, err, strings.TrimSpace(string(out)))
		}
	}
	return nil
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
func restoreArgv(ctx context.Context, agent ports.Agent, id domain.SessionID, workspacePath string, meta domain.SessionMetadata, systemPrompt string, agentConfig ports.AgentConfig) ([]string, error) {
	ref := ports.SessionRef{
		ID:            string(id),
		WorkspacePath: workspacePath,
		Metadata:      map[string]string{ports.MetadataKeyAgentSessionID: meta.AgentSessionID},
	}
	cmd, ok, err := agent.GetRestoreCommand(ctx, ports.RestoreConfig{Session: ref, SystemPrompt: systemPrompt, Config: agentConfig, Permissions: agentConfig.Permissions})
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
		SystemPrompt:  systemPrompt,
		Config:        agentConfig,
		Permissions:   agentConfig.Permissions,
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
