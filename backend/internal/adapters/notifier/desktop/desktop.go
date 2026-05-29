// Package desktop is a macOS-only Notifier port adapter that fires native
// notification-center notifications via osascript. Intended for local-dev /
// testing workflows; Slack/webhook adapters live in sibling packages.
//
// Safety: the AppleScript program defines an `on run argv` handler and reads
// the title and body from argv (passed after `--`). The user payload is never
// composed into the AppleScript source, so AppleScript string-literal
// escaping (\, ", newline) is unnecessary by construction. Go's exec.Command
// does not invoke a shell, so backticks and $() in argv are also inert.
package desktop

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// execer is the seam the adapter uses to invoke osascript. Tests inject a
// programmable fake; production uses realExecer.
type execer interface {
	LookPath(name string) (string, error)
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

type realExecer struct{}

func (realExecer) LookPath(name string) (string, error) { return exec.LookPath(name) }

func (realExecer) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// Notifier implements ports.Notifier by shelling out to /usr/bin/osascript.
type Notifier struct {
	exec execer
}

// New returns a Notifier and verifies that osascript is on PATH.
func New() (*Notifier, error) {
	return newWithExecer(realExecer{})
}

func newWithExecer(e execer) (*Notifier, error) {
	if _, err := e.LookPath("osascript"); err != nil {
		return nil, fmt.Errorf("desktop notifier: osascript not on PATH: %w", err)
	}
	return &Notifier{exec: e}, nil
}

// Notify fires a macOS notification using event.Type as the title and
// event.Message as the body.
func (n *Notifier) Notify(ctx context.Context, event ports.OrchestratorEvent) error {
	out, err := n.exec.Run(ctx, "osascript",
		"-e", "on run argv",
		"-e", "display notification (item 2 of argv) with title (item 1 of argv)",
		"-e", "end run",
		"--", event.Type, event.Message,
	)
	if err != nil {
		trimmed := strings.TrimSpace(string(out))
		if trimmed != "" {
			return fmt.Errorf("desktop notifier: osascript: %w (output: %s)", err, trimmed)
		}
		return fmt.Errorf("desktop notifier: osascript: %w", err)
	}
	return nil
}

var _ ports.Notifier = (*Notifier)(nil)
