package session

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	testProject = domain.ProjectID("proj")
	testIssue   = domain.IssueID("42")
)

func spawnCfg() ports.SpawnConfig {
	return ports.SpawnConfig{
		ProjectID:  testProject,
		IssueID:    testIssue,
		Kind:       domain.KindWorker,
		Branch:     "feat/42",
		Prompt:     "do the thing",
		AgentRules: "be careful",
	}
}

func TestSpawn_HappyPath(t *testing.T) {
	h := newHarness("sess-1")
	ctx := context.Background()

	sess, err := h.sm.Spawn(ctx, spawnCfg())
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}

	// Display status is derived (single producer) — a freshly spawned, not_started
	// session shows as spawning.
	if sess.Status != domain.StatusSpawning {
		t.Errorf("status = %q, want %q", sess.Status, domain.StatusSpawning)
	}

	// Record seeded by the LCM with identity + initial lifecycle, then OnSpawnCompleted flipped
	// the runtime axis to alive.
	rec, ok, err := h.store.Get(ctx, "sess-1")
	if err != nil || !ok {
		t.Fatalf("get seeded record: ok=%v err=%v", ok, err)
	}
	if rec.ProjectID != testProject || rec.IssueID != testIssue || rec.Kind != domain.KindWorker {
		t.Errorf("identity = %+v, want proj/42/worker", rec)
	}
	if !rec.CreatedAt.Equal(fixedTime) {
		t.Errorf("createdAt = %v, want %v", rec.CreatedAt, fixedTime)
	}
	if got := rec.Lifecycle.Session; got.State != domain.SessionNotStarted || got.Reason != domain.ReasonSpawnRequested {
		t.Errorf("session substate = %+v, want not_started/spawn_requested", got)
	}
	if got := rec.Lifecycle.Runtime; got.State != domain.RuntimeAlive || got.Reason != domain.RuntimeReasonProcessRunning {
		t.Errorf("runtime substate = %+v, want alive/process_running", got)
	}

	// Pipeline order: workspace -> runtime -> LCM seed command -> LCM completion.
	wantOrder := []string{"Workspace.Create", "Runtime.Create", "OnSpawnInitiated", "OnSpawnCompleted"}
	if got := h.log.snapshot(); !equalStrings(got, wantOrder) {
		t.Errorf("call order = %v, want %v", got, wantOrder)
	}

	// Identity env wired onto the runtime config, layered over the agent's env.
	if len(h.runtime.created) != 1 {
		t.Fatalf("runtime.created = %d, want 1", len(h.runtime.created))
	}
	env := h.runtime.created[0].Env
	for k, want := range map[string]string{
		EnvSessionID: "sess-1",
		EnvProjectID: "proj",
		EnvIssueID:   "42",
		"BASE":       "1",
	} {
		if env[k] != want {
			t.Errorf("env[%q] = %q, want %q", k, env[k], want)
		}
	}

	// Handles persisted to metadata for later teardown/restore. The prompt is
	// persisted too so a later Restore that finds no captured agent session id
	// can still fall back to a fresh launch using the same prompt.
	meta, _ := h.store.GetMetadata(ctx, "sess-1")
	for k, want := range map[string]string{
		lifecycle.MetaBranch:          "feat/42",
		lifecycle.MetaWorkspacePath:   "/tmp/ws/sess-1",
		lifecycle.MetaRuntimeHandleID: "rt-sess-1",
		lifecycle.MetaRuntimeName:     "tmux",
		lifecycle.MetaPrompt:          "do the thing\n\nbe careful",
	} {
		if meta[k] != want {
			t.Errorf("meta[%q] = %q, want %q", k, meta[k], want)
		}
	}
}

func TestSpawn_RuntimeCreateFailure_RollsBack(t *testing.T) {
	h := newHarness("sess-1")
	ctx := context.Background()
	h.runtime.createErr = errors.New("boom")

	_, err := h.sm.Spawn(ctx, spawnCfg())
	if err == nil {
		t.Fatal("spawn: want error, got nil")
	}

	// No record seeded for a spawn that never completed.
	if _, ok, _ := h.store.Get(ctx, "sess-1"); ok {
		t.Error("record was seeded despite runtime-create failure")
	}
	// The already-created workspace was rolled back (eager rollback), since a
	// late-seeded record means Cleanup could never find this orphan.
	if len(h.workspace.destroyed) != 1 || h.workspace.destroyed[0].Path != "/tmp/ws/sess-1" {
		t.Errorf("workspace.destroyed = %+v, want the created worktree", h.workspace.destroyed)
	}
	// LCM never told a spawn completed.
	if h.log.indexOf("OnSpawnCompleted") != -1 {
		t.Error("OnSpawnCompleted should not fire on a failed spawn")
	}
}

func TestSpawn_ExistingSessionIDRejectedBeforeWork(t *testing.T) {
	h := newHarness("sess-1")
	ctx := context.Background()
	if err := h.store.Upsert(ctx, domain.SessionRecord{
		ID:        "sess-1",
		ProjectID: testProject,
		Lifecycle: lc(domain.SessionWorking, domain.ReasonTaskInProgress, domain.PRNone, ""),
	}, ports.EventSessionCreated); err != nil {
		t.Fatalf("seed existing row: %v", err)
	}

	_, err := h.sm.Spawn(ctx, spawnCfg())
	if err == nil {
		t.Fatal("spawn: want error for existing session id, got nil")
	}
	if len(h.workspace.created) != 0 {
		t.Error("workspace should not be created when session id already exists")
	}
	if len(h.runtime.created) != 0 {
		t.Error("runtime should not be created when session id already exists")
	}
	if h.log.indexOf("OnSpawnInitiated") != -1 || h.log.indexOf("OnSpawnCompleted") != -1 {
		t.Error("LCM should not be called when session id already exists")
	}
}

func TestSpawn_OnSpawnCompletedFailure_RoutesOrphanToErrored(t *testing.T) {
	h := newHarness("sess-1")
	ctx := context.Background()
	h.lcm.onSpawnErr = errors.New("lcm boom")

	_, err := h.sm.Spawn(ctx, spawnCfg())
	if err == nil {
		t.Fatal("spawn: want error, got nil")
	}

	// Runtime + workspace are torn down on the failure path.
	if len(h.runtime.destroyed) != 1 {
		t.Errorf("runtime.destroyed = %d, want 1", len(h.runtime.destroyed))
	}
	if len(h.workspace.destroyed) != 1 {
		t.Errorf("workspace.destroyed = %d, want 1", len(h.workspace.destroyed))
	}
	// The record was already seeded and the store has no delete, so the orphan is
	// routed to a terminal errored state (via OnKillRequested(KillError)) rather
	// than stranded forever as "spawning".
	rec, ok, _ := h.store.Get(ctx, "sess-1")
	if !ok {
		t.Fatal("seeded record vanished; expected it parked as errored")
	}
	if got := rec.Lifecycle.Session; got.State != domain.SessionTerminated || got.Reason != domain.ReasonErrorInProcess {
		t.Errorf("session substate = %+v, want terminated/error_in_process", got)
	}
	if status := domain.DeriveLegacyStatus(rec.Lifecycle); status != domain.StatusErrored {
		t.Errorf("status = %q, want errored", status)
	}
}

func TestKill_OrderingAndTerminalState(t *testing.T) {
	h := newHarness("sess-1")
	ctx := context.Background()
	if _, err := h.sm.Spawn(ctx, spawnCfg()); err != nil {
		t.Fatalf("spawn: %v", err)
	}

	res, err := h.sm.Kill(ctx, "sess-1", ports.KillOptions{Reason: ports.KillManual})
	if err != nil {
		t.Fatalf("kill: %v", err)
	}
	if !res.WorkspaceFreed {
		t.Error("WorkspaceFreed = false, want true")
	}

	// Intent recorded with the LCM BEFORE any teardown, runtime before workspace.
	iKill := h.log.indexOf("OnKillRequested")
	iRT := h.log.indexOf("Runtime.Destroy")
	iWS := h.log.indexOf("Workspace.Destroy")
	if !(iKill >= 0 && iKill < iRT && iRT < iWS) {
		t.Errorf("kill order indices: OnKillRequested=%d Runtime.Destroy=%d Workspace.Destroy=%d (want ascending)", iKill, iRT, iWS)
	}

	// Terminal canonical written by the LCM; display derives to killed.
	rec, _, _ := h.store.Get(ctx, "sess-1")
	if got := rec.Lifecycle.Session; got.State != domain.SessionTerminated || got.Reason != domain.ReasonManuallyKilled {
		t.Errorf("session substate = %+v, want terminated/manually_killed", got)
	}
	if status := domain.DeriveLegacyStatus(rec.Lifecycle); status != domain.StatusKilled {
		t.Errorf("status = %q, want killed", status)
	}
}

func TestKill_WorktreeRemoveRefusalSurfaced(t *testing.T) {
	h := newHarness("sess-1")
	ctx := context.Background()
	if _, err := h.sm.Spawn(ctx, spawnCfg()); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	// The worktree path is still registered after prune (uncommitted work).
	h.workspace.refuse["/tmp/ws/sess-1"] = true

	res, err := h.sm.Kill(ctx, "sess-1", ports.KillOptions{Reason: ports.KillManual})
	if err == nil {
		t.Fatal("kill: want refusal error, got nil")
	}
	if res.WorkspaceFreed {
		t.Error("WorkspaceFreed = true, want false on refusal")
	}
	// The refusal must be honored — the path is never force-deleted.
	if len(h.workspace.destroyed) != 0 {
		t.Errorf("workspace.destroyed = %+v, want none (refused)", h.workspace.destroyed)
	}
	// Runtime still torn down and intent still recorded — only the worktree is spared.
	if h.log.indexOf("Runtime.Destroy") == -1 || h.log.indexOf("OnKillRequested") == -1 {
		t.Error("runtime teardown / kill intent should still happen on a workspace refusal")
	}
}

func TestKill_IncompleteMetadata_RefusesTeardown(t *testing.T) {
	h := newHarness("sess-1")
	ctx := context.Background()
	// A record with no teardown metadata (empty runtime handle + workspace path),
	// e.g. a partially-seeded or corrupted record.
	if err := h.store.Upsert(ctx, domain.SessionRecord{
		ID: "sess-1", ProjectID: testProject,
		Lifecycle: lc(domain.SessionWorking, domain.ReasonTaskInProgress, domain.PRNone, ""),
	}, ports.EventSessionCreated); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	if _, err := h.sm.Kill(ctx, "sess-1", ports.KillOptions{Reason: ports.KillManual}); !errors.Is(err, ErrIncompleteTeardownMetadata) {
		t.Fatalf("kill: err = %v, want ErrIncompleteTeardownMetadata", err)
	}
	// Nothing destroyed with empty args, and no intent recorded.
	if len(h.runtime.destroyed) != 0 || len(h.workspace.destroyed) != 0 {
		t.Errorf("teardown ran despite incomplete metadata: rt=%v ws=%v", h.runtime.destroyed, h.workspace.destroyed)
	}
	if h.log.indexOf("OnKillRequested") != -1 {
		t.Error("kill intent recorded despite incomplete metadata")
	}
}

func TestCleanup_IncompleteMetadata_Skipped(t *testing.T) {
	h := newHarness("unused")
	ctx := context.Background()
	// Terminal session but no workspace path persisted — must be skipped, never
	// handed to Destroy with an empty path.
	if err := h.store.Upsert(ctx, domain.SessionRecord{
		ID: "orphan-1", ProjectID: testProject,
		Lifecycle: lc(domain.SessionTerminated, domain.ReasonManuallyKilled, domain.PRNone, ""),
	}, ports.EventSessionCreated); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	res, err := h.sm.Cleanup(ctx, testProject)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if !equalIDSet(res.Skipped, []domain.SessionID{"orphan-1"}) {
		t.Errorf("skipped = %v, want [orphan-1]", res.Skipped)
	}
	if len(res.Cleaned) != 0 {
		t.Errorf("cleaned = %v, want none", res.Cleaned)
	}
	if len(h.workspace.destroyed) != 0 {
		t.Errorf("workspace.destroyed = %v, want none (empty path must not reach Destroy)", h.workspace.destroyed)
	}
}

func TestRestore_LiveSession_Rejected(t *testing.T) {
	h := newHarness("sess-1")
	ctx := context.Background()
	if _, err := h.sm.Spawn(ctx, spawnCfg()); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	// The session is live (never torn down). Capture an agent id so the only thing
	// blocking restore is the non-terminal lifecycle, not missing metadata.
	if err := h.store.PatchMetadata(ctx, "sess-1", map[string]string{lifecycle.MetaAgentSessionID: "agent-xyz"}); err != nil {
		t.Fatalf("patch metadata: %v", err)
	}
	createdBefore := len(h.runtime.created)
	restoresBefore := len(h.workspace.restoredID)

	if _, err := h.sm.Restore(ctx, "sess-1"); !errors.Is(err, ErrNotRestorable) {
		t.Fatalf("restore: err = %v, want ErrNotRestorable", err)
	}
	// No second runtime/workspace spun up for the still-live session.
	if len(h.runtime.created) != createdBefore {
		t.Error("runtime created for a live-session restore")
	}
	if len(h.workspace.restoredID) != restoresBefore {
		t.Error("workspace restored for a live-session restore")
	}
}

func TestListAndGet_DeriveStatus(t *testing.T) {
	cases := []struct {
		name string
		lc   domain.CanonicalSessionLifecycle
		want domain.SessionStatus
	}{
		{"not_started", lc(domain.SessionNotStarted, domain.ReasonSpawnRequested, domain.PRNone, ""), domain.StatusSpawning},
		{"working", lc(domain.SessionWorking, domain.ReasonTaskInProgress, domain.PRNone, ""), domain.StatusWorking},
		{"idle", lc(domain.SessionIdle, domain.ReasonResearchComplete, domain.PRNone, ""), domain.StatusIdle},
		{"needs_input", lc(domain.SessionNeedsInput, domain.ReasonAwaitingUserInput, domain.PRNone, ""), domain.StatusNeedsInput},
		{"pr_ci_failed", lc(domain.SessionWorking, domain.ReasonFixingCI, domain.PROpen, domain.PRReasonCIFailing), domain.StatusCIFailed},
		{"pr_merged", lc(domain.SessionIdle, domain.ReasonMergedWaitingDecision, domain.PRMerged, domain.PRReasonMerged), domain.StatusMerged},
		{"killed", lc(domain.SessionTerminated, domain.ReasonManuallyKilled, domain.PRNone, ""), domain.StatusKilled},
	}

	h := newHarness("unused")
	ctx := context.Background()
	for _, c := range cases {
		if err := h.store.Upsert(ctx, domain.SessionRecord{ID: domain.SessionID(c.name), ProjectID: testProject, Lifecycle: c.lc}, ports.EventSessionCreated); err != nil {
			t.Fatalf("upsert %s: %v", c.name, err)
		}
	}

	// Get derives per-record.
	for _, c := range cases {
		got, err := h.sm.Get(ctx, domain.SessionID(c.name))
		if err != nil {
			t.Fatalf("get %s: %v", c.name, err)
		}
		if got.Status != c.want {
			t.Errorf("get %s: status = %q, want %q", c.name, got.Status, c.want)
		}
	}

	// List derives for every record in the project.
	got, err := h.sm.List(ctx, testProject)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != len(cases) {
		t.Fatalf("list len = %d, want %d", len(got), len(cases))
	}
	byID := map[domain.SessionID]domain.SessionStatus{}
	for _, s := range got {
		byID[s.ID] = s.Status
	}
	for _, c := range cases {
		if byID[domain.SessionID(c.name)] != c.want {
			t.Errorf("list %s: status = %q, want %q", c.name, byID[domain.SessionID(c.name)], c.want)
		}
	}
}

func TestGet_NotFound(t *testing.T) {
	h := newHarness("sess-1")
	if _, err := h.sm.Get(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("get missing: err = %v, want ErrNotFound", err)
	}
}

func TestSend_RoutesToMessenger(t *testing.T) {
	h := newHarness("sess-1")
	if err := h.sm.Send(context.Background(), "sess-1", "hello"); err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(h.messenger.sent) != 1 || h.messenger.sent[0].ID != "sess-1" || h.messenger.sent[0].Message != "hello" {
		t.Errorf("messenger.sent = %+v, want one {sess-1, hello}", h.messenger.sent)
	}
}

func TestRestore_RelaunchesWithResumeCommand(t *testing.T) {
	h := newHarness("sess-1")
	ctx := context.Background()
	if _, err := h.sm.Spawn(ctx, spawnCfg()); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if _, err := h.sm.Kill(ctx, "sess-1", ports.KillOptions{Reason: ports.KillManual}); err != nil {
		t.Fatalf("kill: %v", err)
	}
	// The agent's resume id is captured in metadata (here set explicitly).
	if err := h.store.PatchMetadata(ctx, "sess-1", map[string]string{lifecycle.MetaAgentSessionID: "agent-xyz"}); err != nil {
		t.Fatalf("patch metadata: %v", err)
	}

	sess, err := h.sm.Restore(ctx, "sess-1")
	if err != nil {
		t.Fatalf("restore: %v", err)
	}

	// Reopened: terminal session reset to a fresh spawn, PR cleared, runtime alive.
	if sess.Status != domain.StatusSpawning {
		t.Errorf("status = %q, want spawning", sess.Status)
	}
	rec, _, _ := h.store.Get(ctx, "sess-1")
	if got := rec.Lifecycle.Session; got.State != domain.SessionNotStarted || got.Reason != domain.ReasonSpawnRequested {
		t.Errorf("session substate = %+v, want not_started/spawn_requested", got)
	}
	if got := rec.Lifecycle.PR; got.State != domain.PRNone || got.Reason != domain.PRReasonClearedOnRestore {
		t.Errorf("pr substate = %+v, want none/cleared_on_restore", got)
	}
	if rec.Lifecycle.Runtime.State != domain.RuntimeAlive {
		t.Errorf("runtime state = %q, want alive", rec.Lifecycle.Runtime.State)
	}

	// Relaunched via the agent's resume command (created[0] is the original spawn).
	if len(h.runtime.created) != 2 {
		t.Fatalf("runtime.created = %d, want 2 (spawn + restore)", len(h.runtime.created))
	}
	if got := h.runtime.created[1].LaunchCommand; got != "claude --resume agent-xyz" {
		t.Errorf("restore launch command = %q, want resume", got)
	}
	if h.log.indexOf("Workspace.Restore") == -1 {
		t.Error("Workspace.Restore was not called")
	}
}

func TestRestore_NoAgentSessionID_FreshLaunchFallback(t *testing.T) {
	h := newHarness("sess-1")
	ctx := context.Background()
	if _, err := h.sm.Spawn(ctx, spawnCfg()); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if _, err := h.sm.Kill(ctx, "sess-1", ports.KillOptions{Reason: ports.KillManual}); err != nil {
		t.Fatalf("kill: %v", err)
	}
	// No agent session id was ever captured (the capture hook is a separate
	// path that may never have run), but Spawn persisted the prompt, so Restore
	// must fall back to a fresh launch instead of failing.
	createdBefore := len(h.runtime.created)

	sess, err := h.sm.Restore(ctx, "sess-1")
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if sess.Status != domain.StatusSpawning {
		t.Errorf("status = %q, want spawning", sess.Status)
	}
	if len(h.runtime.created) != createdBefore+1 {
		t.Fatalf("runtime.created grew by %d, want 1 (fresh-launch fallback)", len(h.runtime.created)-createdBefore)
	}
	// Fresh launch uses GetLaunchCommand (returns "claude" in the fake) — not
	// the resume command, which would have read "claude --resume <id>".
	if got := h.runtime.created[createdBefore].LaunchCommand; got != "claude" {
		t.Errorf("restore launch command = %q, want fresh-launch %q", got, "claude")
	}
}

func TestRestore_NoIDAndNoPrompt_Errors(t *testing.T) {
	h := newHarness("sess-1")
	ctx := context.Background()
	// Seed a terminal record directly without any metadata — no agent session id,
	// no prompt. Restore has nothing to resume and nothing to relaunch from, so
	// it must fail early without touching workspace/runtime.
	if err := h.store.Upsert(ctx, domain.SessionRecord{
		ID: "sess-1", ProjectID: testProject,
		Lifecycle: lc(domain.SessionTerminated, domain.ReasonManuallyKilled, domain.PRNone, ""),
	}, ports.EventSessionCreated); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	beforeRestores := len(h.workspace.restoredID)
	beforeCreated := len(h.runtime.created)

	if _, err := h.sm.Restore(ctx, "sess-1"); err == nil {
		t.Fatal("restore: want error for missing agent session id and prompt, got nil")
	}
	if len(h.workspace.restoredID) != beforeRestores {
		t.Error("workspace was touched despite a doomed restore")
	}
	if len(h.runtime.created) != beforeCreated {
		t.Error("runtime was created despite a doomed restore")
	}
	// The session stays terminal — a failed restore does not reopen it.
	rec, _, _ := h.store.Get(ctx, "sess-1")
	if rec.Lifecycle.Session.State != domain.SessionTerminated {
		t.Errorf("session state = %q, want terminated (unchanged)", rec.Lifecycle.Session.State)
	}
}

func TestRestore_OnSpawnCompletedFailure_RollsBackRuntime(t *testing.T) {
	h := newHarness("sess-1")
	ctx := context.Background()
	if _, err := h.sm.Spawn(ctx, spawnCfg()); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if _, err := h.sm.Kill(ctx, "sess-1", ports.KillOptions{Reason: ports.KillManual}); err != nil {
		t.Fatalf("kill: %v", err)
	}
	if err := h.store.PatchMetadata(ctx, "sess-1", map[string]string{lifecycle.MetaAgentSessionID: "agent-xyz"}); err != nil {
		t.Fatalf("patch metadata: %v", err)
	}
	beforeMeta, _ := h.store.GetMetadata(ctx, "sess-1")

	// Fail the post-create LCM call; capture teardown counts just before restore.
	h.lcm.onSpawnErr = errors.New("lcm boom")
	before, _, _ := h.store.Get(ctx, "sess-1")
	destroyedBefore := len(h.runtime.destroyed)
	wsDestroyedBefore := len(h.workspace.destroyed)

	if _, err := h.sm.Restore(ctx, "sess-1"); err == nil {
		t.Fatal("restore: want error, got nil")
	}

	rec, _, _ := h.store.Get(ctx, "sess-1")
	if got := rec.Lifecycle.Session; got.State != domain.SessionTerminated || got.Reason != domain.ReasonManuallyKilled {
		t.Fatalf("restore failure should restore terminal lifecycle, got %+v", got)
	}
	if rec.Lifecycle.Revision != before.Lifecycle.Revision+2 {
		t.Fatalf("restore failure should advance revision twice, got %d want %d", rec.Lifecycle.Revision, before.Lifecycle.Revision+2)
	}
	afterMeta, _ := h.store.GetMetadata(ctx, "sess-1")
	if !equalStringMap(afterMeta, beforeMeta) {
		t.Fatalf("restore failure should restore metadata, got %+v want %+v", afterMeta, beforeMeta)
	}

	// The runtime created during restore is torn back down so no process is
	// stranded; the workspace is left intact (it holds the agent's prior work).
	if len(h.runtime.destroyed) != destroyedBefore+1 {
		t.Errorf("runtime.destroyed grew by %d, want 1 (restore rollback)", len(h.runtime.destroyed)-destroyedBefore)
	}
	if len(h.workspace.destroyed) != wsDestroyedBefore {
		t.Errorf("workspace was destroyed on restore rollback; it must be preserved")
	}
}

func TestCleanup_SkipsUncommittedWork(t *testing.T) {
	h := newHarness("unused")
	ctx := context.Background()

	// Two terminal sessions (reclaimable) + one working session (must be ignored).
	seedTerminal(t, h, "done-1", "/tmp/ws/done-1")
	seedTerminal(t, h, "dirty-1", "/tmp/ws/dirty-1")
	if err := h.store.Upsert(ctx, domain.SessionRecord{
		ID: "live-1", ProjectID: testProject,
		Lifecycle: lc(domain.SessionWorking, domain.ReasonTaskInProgress, domain.PRNone, ""),
	}, ports.EventSessionCreated); err != nil {
		t.Fatalf("upsert live: %v", err)
	}
	// dirty-1's worktree still holds uncommitted work — Destroy refuses it.
	h.workspace.refuse["/tmp/ws/dirty-1"] = true

	res, err := h.sm.Cleanup(ctx, testProject)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	if !equalIDSet(res.Cleaned, []domain.SessionID{"done-1"}) {
		t.Errorf("cleaned = %v, want [done-1]", res.Cleaned)
	}
	if !equalIDSet(res.Skipped, []domain.SessionID{"dirty-1"}) {
		t.Errorf("skipped = %v, want [dirty-1]", res.Skipped)
	}
	// The live session was never a candidate.
	if contains(res.Cleaned, "live-1") || contains(res.Skipped, "live-1") {
		t.Error("non-terminal session must not be cleaned or skipped")
	}
}

// ---- test helpers ----

func lc(s domain.SessionState, r domain.SessionReason, prs domain.PRState, prr domain.PRReason) domain.CanonicalSessionLifecycle {
	return domain.CanonicalSessionLifecycle{
		Version: domain.LifecycleVersion,
		Session: domain.SessionSubstate{State: s, Reason: r},
		PR:      domain.PRSubstate{State: prs, Reason: prr},
		Runtime: domain.RuntimeSubstate{State: domain.RuntimeAlive, Reason: domain.RuntimeReasonProcessRunning},
	}
}

func seedTerminal(t *testing.T, h *harness, id domain.SessionID, wsPath string) {
	t.Helper()
	ctx := context.Background()
	if err := h.store.Upsert(ctx, domain.SessionRecord{
		ID: id, ProjectID: testProject,
		Lifecycle: lc(domain.SessionTerminated, domain.ReasonManuallyKilled, domain.PRNone, ""),
	}, ports.EventSessionCreated); err != nil {
		t.Fatalf("upsert %s: %v", id, err)
	}
	if err := h.store.PatchMetadata(ctx, id, map[string]string{lifecycle.MetaWorkspacePath: wsPath}); err != nil {
		t.Fatalf("patch metadata %s: %v", id, err)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalStringMap(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func contains(ids []domain.SessionID, id domain.SessionID) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

func equalIDSet(got, want []domain.SessionID) bool {
	if len(got) != len(want) {
		return false
	}
	for _, w := range want {
		if !contains(got, w) {
			return false
		}
	}
	return true
}
