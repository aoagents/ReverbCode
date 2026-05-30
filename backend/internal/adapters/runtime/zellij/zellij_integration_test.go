package zellij

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestRuntimeIntegration(t *testing.T) {
	if _, err := exec.LookPath("zellij"); err != nil {
		t.Skip("zellij unavailable")
	}

	ctx := context.Background()
	id := "ao_itest_zj"
	socketDir := filepath.Join(os.TempDir(), "ao-zj-itest")
	if err := os.MkdirAll(socketDir, 0o755); err != nil {
		t.Fatalf("mkdir socket dir: %v", err)
	}
	configDir := t.TempDir()
	r := New(Options{Timeout: 5 * time.Second, SocketDir: socketDir, ConfigDir: configDir})
	_ = r.Destroy(ctx, ports.RuntimeHandle{ID: id, RuntimeName: runtimeName})

	h, err := r.Create(ctx, ports.RuntimeConfig{
		SessionID:     "ao_itest_zj",
		WorkspacePath: t.TempDir(),
		LaunchCommand: "printf ready-$AO_SESSION_ID\\n",
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

	if err := r.SendMessage(ctx, h, "printf hello-from-zellij"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	var out string
	for time.Now().Before(deadline) {
		out, err = r.GetOutput(ctx, h, 30)
		if err != nil {
			t.Fatalf("GetOutput: %v", err)
		}
		if strings.Contains(out, "hello-from-zellij") {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !strings.Contains(out, "hello-from-zellij") {
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

func TestRuntimeIntegrationUsesExactSessionParsing(t *testing.T) {
	if _, err := exec.LookPath("zellij"); err != nil {
		t.Skip("zellij unavailable")
	}

	ctx := context.Background()
	socketDir := filepath.Join(os.TempDir(), "ao-zj-exact-itest")
	if err := os.MkdirAll(socketDir, 0o755); err != nil {
		t.Fatalf("mkdir socket dir: %v", err)
	}
	r := New(Options{Timeout: 5 * time.Second, SocketDir: socketDir, ConfigDir: t.TempDir()})
	longID := "ao_zj_exact_long"
	prefixID := "ao_zj_exact"
	_ = r.Destroy(ctx, ports.RuntimeHandle{ID: longID, RuntimeName: runtimeName})
	_ = r.Destroy(ctx, ports.RuntimeHandle{ID: prefixID, RuntimeName: runtimeName})

	h, err := r.Create(ctx, ports.RuntimeConfig{
		SessionID:     "ao_zj_exact_long",
		WorkspacePath: t.TempDir(),
		LaunchCommand: "printf ready\\n",
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
		t.Fatal("prefix handle reported alive; zellij session matching is not exact")
	}
}
