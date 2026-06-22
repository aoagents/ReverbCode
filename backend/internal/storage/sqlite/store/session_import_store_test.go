package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestImportSessionVerbatimAndIdempotent(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")

	now := time.Now().UTC().Truncate(time.Second)
	rec := domain.SessionRecord{
		ID:        "mer-orchestrator",
		ProjectID: "mer",
		Kind:      domain.KindOrchestrator,
		Harness:   domain.HarnessClaudeCode,
		Activity:  domain.Activity{State: domain.ActivityIdle, LastActivityAt: now},
		Metadata:  domain.SessionMetadata{AgentSessionID: "uuid-1", Prompt: "go"},
		CreatedAt: now,
		UpdatedAt: now,
	}

	inserted, err := s.ImportSession(ctx, rec, 0)
	if err != nil || !inserted {
		t.Fatalf("first import: inserted=%v err=%v", inserted, err)
	}

	got, ok, err := s.GetSession(ctx, "mer-orchestrator")
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.Kind != domain.KindOrchestrator || got.Metadata.AgentSessionID != "uuid-1" {
		t.Fatalf("imported row = %+v", got)
	}

	// Re-import is a no-op: the existing row is left untouched.
	inserted, err = s.ImportSession(ctx, rec, 0)
	if err != nil {
		t.Fatalf("re-import err: %v", err)
	}
	if inserted {
		t.Fatal("re-import reported inserted=true; want false (idempotent skip)")
	}

	// num=0 leaves the next store-generated session at num=1 with no collision.
	w, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatalf("create worker: %v", err)
	}
	if w.ID != "mer-1" {
		t.Fatalf("worker id = %s, want mer-1 (orchestrator at num 0 must not collide)", w.ID)
	}
}
