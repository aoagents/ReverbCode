package tmux

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestRuntimeIntegration(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}

	r := New(Options{Timeout: 5 * time.Second})
	ctx := context.Background()
	id := "ao_itest_tmux"
	_ = r.Destroy(ctx, ports.RuntimeHandle{ID: id, RuntimeName: runtimeName})

	h, err := r.Create(ctx, ports.RuntimeConfig{
		SessionID:     "ao_itest_tmux",
		WorkspacePath: t.TempDir(),
		LaunchCommand: "printf ready\\n",
		Env:           map[string]string{"AO_SESSION_ID": id},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer r.Destroy(ctx, h)

	alive, err := r.IsAlive(ctx, h)
	if err != nil {
		t.Fatalf("IsAlive: %v", err)
	}
	if !alive {
		t.Fatal("alive = false, want true")
	}

	if err := r.SendMessage(ctx, h, "printf hello-from-tmux"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	var out string
	for time.Now().Before(deadline) {
		out, err = r.GetOutput(ctx, h, 20)
		if err != nil {
			t.Fatalf("GetOutput: %v", err)
		}
		if strings.Contains(out, "hello-from-tmux") {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !strings.Contains(out, "hello-from-tmux") {
		t.Fatalf("output = %q, want sent command output", out)
	}

	if err := r.Destroy(ctx, h); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	alive, err = r.IsAlive(ctx, h)
	if err != nil {
		t.Fatalf("IsAlive after destroy: %v", err)
	}
	if alive {
		t.Fatal("alive after destroy = true, want false")
	}
}

func TestRuntimeIntegrationUsesExactTargets(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}

	r := New(Options{Timeout: 5 * time.Second})
	ctx := context.Background()
	longID := "ao_exact_target_long"
	prefixID := "ao_exact_target"
	_ = r.Destroy(ctx, ports.RuntimeHandle{ID: longID, RuntimeName: runtimeName})
	_ = r.Destroy(ctx, ports.RuntimeHandle{ID: prefixID, RuntimeName: runtimeName})

	h, err := r.Create(ctx, ports.RuntimeConfig{
		SessionID:     "ao_exact_target_long",
		WorkspacePath: t.TempDir(),
		LaunchCommand: "cat",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer r.Destroy(ctx, h)

	alive, err := r.IsAlive(ctx, ports.RuntimeHandle{ID: prefixID, RuntimeName: runtimeName})
	if err != nil {
		t.Fatalf("IsAlive prefix: %v", err)
	}
	if alive {
		t.Fatal("prefix handle reported alive; tmux target matching is not exact")
	}
	if err := r.Destroy(ctx, ports.RuntimeHandle{ID: prefixID, RuntimeName: runtimeName}); err != nil {
		t.Fatalf("Destroy prefix: %v", err)
	}
	alive, err = r.IsAlive(ctx, h)
	if err != nil {
		t.Fatalf("IsAlive long after prefix destroy: %v", err)
	}
	if !alive {
		t.Fatal("destroying prefix handle killed longer session")
	}
}
