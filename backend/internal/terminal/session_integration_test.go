//go:build !windows

package terminal

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/zellij"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// TestSessionStreamsRealZellijPane attaches a real PTY to a real Zellij session and
// asserts output streams back, then that killing the pane stops the session
// without a re-attach storm. Skipped when Zellij is unavailable.
func TestSessionStreamsRealZellijPane(t *testing.T) {
	zellijBin, err := exec.LookPath("zellij")
	if err != nil {
		t.Skip("zellij unavailable")
	}

	name := "ao-term-it-" + strconv.Itoa(os.Getpid())
	socketDir := filepath.Join("/tmp", name+"-socket")
	if err := os.MkdirAll(socketDir, 0o755); err != nil {
		t.Fatalf("mkdir socket dir: %v", err)
	}
	rt := zellij.New(zellij.Options{Binary: zellijBin, SocketDir: socketDir, ConfigDir: t.TempDir(), Timeout: 5 * time.Second})
	handle, err := rt.Create(context.Background(), ports.RuntimeConfig{
		SessionID:     domain.SessionID(name),
		WorkspacePath: t.TempDir(),
		Argv:          []string{"printf", "AO_READY\\n"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _ = rt.Destroy(context.Background(), handle) })

	s := newSession(name, handle, rt, defaultSpawn, testLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.run(ctx)

	var got safeBytes
	s.subscribe(got.add, nil)

	// Type a unique marker and expect it echoed back through the PTY.
	eventually(t, 3*time.Second, func() bool { return s.write([]byte("echo AO_MARKER_42\n")) == nil })
	eventually(t, 5*time.Second, func() bool { return strings.Contains(got.string(), "AO_MARKER_42") })

	// Kill the session: the terminal session must observe it as gone and not re-attach.
	if err := rt.Destroy(context.Background(), handle); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	eventually(t, 5*time.Second, func() bool { return s.isExited() })
}
