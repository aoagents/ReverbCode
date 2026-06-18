package store_test

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

func TestAgentDefaultsRoundTrip(t *testing.T) {
	st, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	ctx := context.Background()
	if got, ok, err := st.GetAgentDefaults(ctx); err != nil || ok || got.Complete() {
		t.Fatalf("initial defaults got=%+v ok=%v err=%v, want unset", got, ok, err)
	}

	want := domain.AgentDefaults{
		DefaultWorkerAgent:       domain.HarnessCodex,
		DefaultOrchestratorAgent: domain.HarnessClaudeCode,
	}
	if err := st.SetAgentDefaults(ctx, want); err != nil {
		t.Fatalf("SetAgentDefaults: %v", err)
	}
	got, ok, err := st.GetAgentDefaults(ctx)
	if err != nil {
		t.Fatalf("GetAgentDefaults: %v", err)
	}
	if !ok || got != want {
		t.Fatalf("defaults got=%+v ok=%v, want %+v", got, ok, want)
	}
}
