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

// TestAttachmentStreamsRealZellijPane attaches a real PTY to a real Zellij
// session and asserts output streams back, then that killing the pane stops the
// attachment without a re-attach storm. Skipped when Zellij is unavailable.
func TestAttachmentStreamsRealZellijPane(t *testing.T) {
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

	var got safeBytes
	a := newAttachment(name, handle, rt, defaultSpawn, got.add, nil, testLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.run(ctx)

	// Type a unique marker and expect it echoed back through the PTY.
	eventually(t, 3*time.Second, func() bool { return a.write([]byte("echo AO_MARKER_42\n")) == nil })
	eventually(t, 5*time.Second, func() bool { return strings.Contains(got.string(), "AO_MARKER_42") })

	// A fresh attach must carry zellij's init handshake: alt screen + SGR mouse
	// tracking. This is the whole point of per-client attach — late clients see
	// the mode negotiation, so wheel events are forwarded as mouse reports
	// instead of going dead.
	eventually(t, 5*time.Second, func() bool {
		out := got.string()
		return strings.Contains(out, "\x1b[?1049h") && strings.Contains(out, "\x1b[?1006h")
	})

	// Kill the session: the attachment must observe it as gone and not re-attach.
	if err := rt.Destroy(context.Background(), handle); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	eventually(t, 5*time.Second, func() bool { return a.isExited() })
}

// TestAttachmentReattachAdoptsNewSize is the end-to-end regression for the
// stale-size desync: client A holds the session at one grid, detaches, and
// client B immediately attaches at a different grid (the frontend's
// remount/reconnect flow). B's zellij client must adopt B's size — the inner
// pane's tty must report it — not stay laid out for A's. This is where the
// spawn-at-size + explicit-WINCH + SIGTERM-detach fixes meet a real zellij.
func TestAttachmentReattachAdoptsNewSize(t *testing.T) {
	zellijBin, err := exec.LookPath("zellij")
	if err != nil {
		t.Skip("zellij unavailable")
	}

	name := "ao-term-size-it-" + strconv.Itoa(os.Getpid())
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

	attachAt := func(rows, cols uint16) (*attachment, *safeBytes, context.CancelFunc) {
		var got safeBytes
		a := newAttachment(name, handle, rt, defaultSpawn, got.add, nil, testLogger())
		if err := a.resize(rows, cols); err != nil {
			t.Fatalf("record size: %v", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		go a.run(ctx)
		return a, &got, cancel
	}

	// Client A at 115x37: wait for the pane shell, then detach.
	a, gotA, cancelA := attachAt(37, 115)
	eventually(t, 5*time.Second, func() bool { return a.write([]byte("\n")) == nil })
	eventually(t, 5*time.Second, func() bool { return strings.Contains(gotA.string(), "AO_READY") })
	a.close()
	cancelA()

	// Client B re-attaches immediately at 148x40 — no settle gap, same as the
	// frontend reconnecting. The inner pane must see B's grid (zellij chrome
	// shaves a couple rows/cols, so assert the reported cols land near 148 and
	// far from 115).
	b, gotB, cancelB := attachAt(40, 148)
	defer cancelB()
	defer b.close()

	eventually(t, 5*time.Second, func() bool { return b.write([]byte("echo SIZE:$(stty size)\n")) == nil })
	eventually(t, 10*time.Second, func() bool {
		out := gotB.string()
		i := strings.LastIndex(out, "SIZE:")
		if i < 0 {
			return false
		}
		fields := strings.Fields(strings.TrimPrefix(out[i:], "SIZE:"))
		if len(fields) < 2 {
			return false
		}
		cols, err := strconv.Atoi(strings.TrimFunc(fields[1], func(r rune) bool { return r < '0' || r > '9' }))
		if err != nil {
			return false
		}
		return cols > 130 // B's 148 minus zellij chrome; a stale A-layout reports ≤115
	})
}
