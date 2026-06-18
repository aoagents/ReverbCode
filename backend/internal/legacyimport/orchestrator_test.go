package legacyimport

import (
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func mtime() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }

func TestMapOrchestrator_ClaudeMapped(t *testing.T) {
	raw := map[string]any{
		"agent":             "claude-code",
		"role":              "orchestrator",
		"branch":            "main",
		"worktree":          "/legacy/wt",
		"userPrompt":        "orchestrate",
		"displayName":       "Orch",
		"claudeSessionUuid": "uuid-123",
		"createdAt":         "2026-01-01T00:00:00Z",
		"lifecycle": map[string]any{
			"session": map[string]any{
				"state":            "working",
				"lastTransitionAt": "2026-01-01T01:00:00Z",
			},
		},
	}
	m := mapOrchestratorRecord(raw, "proj", "proj", mtime())
	if m.status != orchMapped {
		t.Fatalf("status = %s, want mapped (note=%q)", m.status, m.note)
	}
	r := m.record
	if r.ID != "proj-orchestrator" || r.Kind != domain.KindOrchestrator {
		t.Fatalf("id/kind = %s/%s", r.ID, r.Kind)
	}
	if r.Activity.State != domain.ActivityActive {
		t.Fatalf("activity = %s, want active", r.Activity.State)
	}
	if r.Metadata.AgentSessionID != "uuid-123" {
		t.Fatalf("agentSessionID = %q, want uuid-123", r.Metadata.AgentSessionID)
	}
	if r.CreatedAt.Format(time.RFC3339) != "2026-01-01T00:00:00Z" {
		t.Fatalf("createdAt = %s", r.CreatedAt)
	}
	if m.transcript == nil || m.transcript.uuid != "uuid-123" || m.transcript.worktree != "/legacy/wt" {
		t.Fatalf("transcript = %+v", m.transcript)
	}
}

func TestMapOrchestrator_DoubleDecodedLifecycle(t *testing.T) {
	// lifecycle stored as a JSON-encoded string (legacy double-encoding).
	raw := map[string]any{
		"agent":         "codex",
		"worktree":      "/wt",
		"createdAt":     "2026-01-01T00:00:00Z",
		"lifecycle":     `{"session":{"state":"needs_input","lastTransitionAt":"2026-01-01T02:00:00Z"}}`,
		"codexThreadId": "thread-9",
		"codexModel":    "o3",
	}
	m := mapOrchestratorRecord(raw, "p", "pre", mtime())
	if m.status != orchMapped {
		t.Fatalf("status = %s", m.status)
	}
	if m.record.Activity.State != domain.ActivityWaitingInput {
		t.Fatalf("state = %s, want waiting_input", m.record.Activity.State)
	}
	if m.record.Metadata.AgentSessionID != "thread-9" {
		t.Fatalf("agentSessionID = %q, want thread-9", m.record.Metadata.AgentSessionID)
	}
	if m.transcript != nil {
		t.Fatal("codex must not carry a transcript relocation")
	}
	if m.note == "" {
		t.Fatal("expected a note about dropped codexModel")
	}
}

func TestMapOrchestrator_StatePayloadFallback(t *testing.T) {
	raw := map[string]any{
		"agent":             "opencode",
		"stateVersion":      "2",
		"statePayload":      map[string]any{"session": map[string]any{"state": "idle"}},
		"opencodeSessionId": "oc-1",
	}
	m := mapOrchestratorRecord(raw, "p", "p", mtime())
	if m.status != orchMapped || m.record.Activity.State != domain.ActivityIdle {
		t.Fatalf("mapping = %+v", m)
	}
	if m.record.Metadata.AgentSessionID != "oc-1" {
		t.Fatalf("agentSessionID = %q", m.record.Metadata.AgentSessionID)
	}
}

func TestMapOrchestrator_StatePayloadFallbackNumericVersion(t *testing.T) {
	// stateVersion as a JSON number (decodes to float64) must still trigger the
	// statePayload fallback.
	raw := map[string]any{
		"agent":             "opencode",
		"stateVersion":      float64(2),
		"statePayload":      map[string]any{"session": map[string]any{"state": "needs_input"}},
		"opencodeSessionId": "oc-2",
	}
	m := mapOrchestratorRecord(raw, "p", "p", mtime())
	if m.status != orchMapped || m.record.Activity.State != domain.ActivityWaitingInput {
		t.Fatalf("mapping = %+v", m)
	}
}

func TestMapOrchestrator_SkipTerminal(t *testing.T) {
	for _, st := range []string{"done", "terminated"} {
		raw := map[string]any{
			"agent":     "claude-code",
			"lifecycle": map[string]any{"session": map[string]any{"state": st}},
		}
		if m := mapOrchestratorRecord(raw, "p", "p", mtime()); m.status != orchSkipped {
			t.Fatalf("state %s: status = %s, want skipped", st, m.status)
		}
	}
}

func TestMapOrchestrator_SkipTerminatedAt(t *testing.T) {
	raw := map[string]any{
		"agent": "claude-code",
		"lifecycle": map[string]any{"session": map[string]any{
			"state": "working", "terminatedAt": "2026-01-01T00:00:00Z",
		}},
	}
	if m := mapOrchestratorRecord(raw, "p", "p", mtime()); m.status != orchSkipped {
		t.Fatalf("status = %s, want skipped (terminatedAt set)", m.status)
	}
}

func TestMapOrchestrator_SkipAiderAndUnknown(t *testing.T) {
	for _, agent := range []string{"aider", "grok", "", "bogus"} {
		raw := map[string]any{
			"agent":     agent,
			"lifecycle": map[string]any{"session": map[string]any{"state": "working"}},
		}
		if m := mapOrchestratorRecord(raw, "p", "p", mtime()); m.status != orchSkipped {
			t.Fatalf("agent %q: status = %s, want skipped", agent, m.status)
		}
	}
}

func TestMapOrchestrator_TimestampFallbacks(t *testing.T) {
	// No createdAt/startedAt → file mtime; no lastTransitionAt → createdAt.
	raw := map[string]any{
		"agent":     "claude-code",
		"lifecycle": map[string]any{"session": map[string]any{"state": "idle"}},
	}
	m := mapOrchestratorRecord(raw, "p", "p", mtime())
	if !m.record.CreatedAt.Equal(mtime()) {
		t.Fatalf("createdAt = %s, want file mtime", m.record.CreatedAt)
	}
	if !m.record.Activity.LastActivityAt.Equal(mtime()) {
		t.Fatalf("activityLastAt = %s, want createdAt fallback", m.record.Activity.LastActivityAt)
	}
}

func TestResolveOrchestratorPrefix(t *testing.T) {
	if got := resolveOrchestratorPrefix("short", legacyProjectConfig{}); got != "short" {
		t.Fatalf("prefix = %q, want short", got)
	}
	if got := resolveOrchestratorPrefix("averylongprojectid", legacyProjectConfig{}); got != "averylongpro" {
		t.Fatalf("prefix = %q, want first 12 chars", got)
	}
	if got := resolveOrchestratorPrefix("proj", legacyProjectConfig{SessionPrefix: "custom"}); got != "custom" {
		t.Fatalf("prefix = %q, want custom", got)
	}
}
