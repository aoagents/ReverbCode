package zellij

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	socketDir := tempSocketDir(t, "ao-zj-itest-")
	configDir := t.TempDir()
	opts := Options{Timeout: 5 * time.Second, SocketDir: socketDir, ConfigDir: configDir}
	if runtime.GOOS == "windows" {
		opts.Timeout = 30 * time.Second
		opts.LauncherBinary = buildAOForIntegration(t)
		opts.Shell = "cmd.exe"
	}
	r := New(opts)
	_ = r.Destroy(ctx, ports.RuntimeHandle{ID: id})
	argv := []string{"sh", "-lc", "printf ready-$AO_SESSION_ID\\n; exec sh -i"}
	sendCommand := "echo hello-from-zellij"
	if runtime.GOOS == "windows" {
		argv = []string{"cmd.exe", "/D", "/Q", "/K", "echo ready-%AO_SESSION_ID%"}
	}

	h, err := r.Create(ctx, ports.RuntimeConfig{
		SessionID:     "ao_itest_zj",
		WorkspacePath: t.TempDir(),
		Argv:          argv,
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
	prefixAlive, err := r.IsAlive(ctx, ports.RuntimeHandle{ID: "ao_itest"})
	if err != nil {
		t.Fatalf("IsAlive prefix: %v", err)
	}
	if prefixAlive {
		t.Fatal("prefix handle reported alive; zellij session matching is not exact")
	}

	out := waitForRuntimeOutput(t, r, h, "ready-")
	if !strings.Contains(out, "ready-") {
		t.Fatalf("output = %q, want ready output", out)
	}
	if err := r.SendMessage(ctx, h, sendCommand); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	out = waitForRuntimeOutput(t, r, h, "hello-from-zellij")
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

func waitForRuntimeOutput(t *testing.T, r *Runtime, h ports.RuntimeHandle, want string) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var out string
	for time.Now().Before(deadline) {
		var err error
		out, err = r.GetOutput(context.Background(), h, 30)
		if err != nil {
			t.Fatalf("GetOutput: %v", err)
		}
		if strings.Contains(out, want) {
			return out
		}
		time.Sleep(100 * time.Millisecond)
	}
	return out
}

func buildAOForIntegration(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "ao.exe")
	cmd := exec.Command("go", "build", "-o", out, "./cmd/ao")
	cmd.Dir = filepath.Clean(filepath.Join("..", "..", "..", ".."))
	if raw, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build ao test launcher: %v: %s", err, strings.TrimSpace(string(raw)))
	}
	return out
}

func tempSocketDir(t *testing.T, pattern string) string {
	t.Helper()
	parent := os.TempDir()
	if runtime.GOOS != "windows" {
		parent = "/tmp"
	}
	socketDir, err := os.MkdirTemp(parent, pattern)
	if err != nil {
		t.Fatalf("mkdir socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	return socketDir
}

func TestRuntimeIntegrationUsesExactSessionParsing(t *testing.T) {
	if _, err := exec.LookPath("zellij"); err != nil {
		t.Skip("zellij unavailable")
	}
	if runtime.GOOS == "windows" {
		t.Skip("exact session parsing is covered by TestRuntimeIntegration on Windows")
	}

	ctx := context.Background()
	socketDir := tempSocketDir(t, "ao-zj-exact-itest-")
	r := New(Options{Timeout: 5 * time.Second, SocketDir: socketDir, ConfigDir: t.TempDir()})
	longID := "ao_zj_exact_long"
	prefixID := "ao_zj_exact"
	_ = r.Destroy(ctx, ports.RuntimeHandle{ID: longID})
	_ = r.Destroy(ctx, ports.RuntimeHandle{ID: prefixID})

	h, err := r.Create(ctx, ports.RuntimeConfig{
		SessionID:     "ao_zj_exact_long",
		WorkspacePath: t.TempDir(),
		Argv:          []string{"sh", "-lc", "printf ready\\n; exec sh -i"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer r.Destroy(ctx, h)

	alive, err := r.IsAlive(ctx, ports.RuntimeHandle{ID: prefixID})
	if err != nil {
		t.Fatalf("IsAlive prefix: %v", err)
	}
	if alive {
		t.Fatal("prefix handle reported alive; zellij session matching is not exact")
	}
}
