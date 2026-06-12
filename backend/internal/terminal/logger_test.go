package terminal

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestNilLoggerFallsBackToDefault(t *testing.T) {
	mgr := NewManager(&fakeSource{}, nil, nil, WithSpawn((&fakeSpawner{}).spawn))
	defer mgr.Close()
	if mgr.log == nil {
		t.Fatal("manager logger is nil")
	}
	a := newAttachment("t1", ports.RuntimeHandle{ID: "t1"}, &fakeSource{}, (&fakeSpawner{}).spawn, nil, nil, nil)
	if a.log == nil {
		t.Fatal("attachment logger is nil")
	}
}
