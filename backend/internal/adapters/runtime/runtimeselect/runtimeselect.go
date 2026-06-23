// Package runtimeselect picks the correct runtime backend by platform:
// tmux on Darwin/Linux, zellij on Windows (interim; ConPTY adapter is a later phase).
package runtimeselect

import (
	"context"
	"log/slog"
	"os"
	"runtime"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/tmux"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/zellij"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Runtime is the union interface that both tmux and zellij satisfy.
// It extends ports.Runtime (Create/Destroy/IsAlive) with the additional methods
// the daemon wires directly, including ports.Attacher (Attach) so the terminal
// layer can open a Stream against the selected runtime.
type Runtime interface {
	ports.Runtime // Create, Destroy, IsAlive
	ports.Attacher
	SendMessage(ctx context.Context, handle ports.RuntimeHandle, message string) error
	GetOutput(ctx context.Context, handle ports.RuntimeHandle, lines int) (string, error)
}

// Compile-time assertions: both adapters must implement the union interface.
var _ Runtime = (*tmux.Runtime)(nil)
var _ Runtime = (*zellij.Runtime)(nil)

// New returns the per-platform runtime: tmux on Darwin/Linux, zellij on Windows.
// log is used only for the Windows zellij socket-dir warning; nil is safe.
func New(log *slog.Logger) Runtime {
	if runtime.GOOS != "windows" {
		return tmux.New(tmux.Options{})
	}
	// ponytail: mirrors daemon.go's zellij socket-dir setup; Windows ConPTY replaces this in a later phase.
	dir := zellij.DefaultSocketDir()
	if dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			if log != nil {
				log.Warn("could not create zellij socket dir; spawns may fail", "dir", dir, "error", err)
			}
		}
	}
	return zellij.New(zellij.Options{SocketDir: dir})
}
