package settings

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

type fakeStore struct {
	defaults domain.AgentDefaults
	ok       bool
	err      error
	saved    domain.AgentDefaults
}

func (f *fakeStore) GetAgentDefaults(context.Context) (domain.AgentDefaults, bool, error) {
	return f.defaults, f.ok, f.err
}

func (f *fakeStore) SetAgentDefaults(_ context.Context, defaults domain.AgentDefaults) error {
	f.saved = defaults
	return f.err
}

func TestGetAgentDefaultsReturnsZeroWhenUnset(t *testing.T) {
	got, err := New(&fakeStore{}).GetAgentDefaults(context.Background())
	if err != nil {
		t.Fatalf("GetAgentDefaults: %v", err)
	}
	if got.Complete() {
		t.Fatalf("defaults = %+v, want first-run incomplete defaults", got)
	}
}

func TestSetAgentDefaultsValidatesAndPersists(t *testing.T) {
	store := &fakeStore{}
	defaults := domain.AgentDefaults{
		DefaultWorkerAgent:       domain.HarnessCodex,
		DefaultOrchestratorAgent: domain.HarnessClaudeCode,
	}
	got, err := New(store).SetAgentDefaults(context.Background(), defaults)
	if err != nil {
		t.Fatalf("SetAgentDefaults: %v", err)
	}
	if got != defaults || store.saved != defaults {
		t.Fatalf("got=%+v saved=%+v, want %+v", got, store.saved, defaults)
	}
}

func TestSetAgentDefaultsRejectsMissingValues(t *testing.T) {
	_, err := New(&fakeStore{}).SetAgentDefaults(context.Background(), domain.AgentDefaults{DefaultWorkerAgent: domain.HarnessCodex})
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Kind != apierr.KindInvalid || apiErr.Code != "INVALID_AGENT_DEFAULTS" {
		t.Fatalf("err = %v, want INVALID_AGENT_DEFAULTS", err)
	}
}
