// Package crush implements the Crush agent adapter: launching new sessions,
// resuming sessions by native ID, and reading session info.
//
// Crush differs from other agents in that it doesn't have full hooks support,
// so GetAgentHooks and SessionInfo are no-ops for now. Session tracking is
// done through basic session ID management only.
package crush

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	// adapterID is the registry id and the value users pass to
	// `ao spawn --agent`. It matches domain.HarnessCrush.
	adapterID = "crush"
)

// Plugin is the Crush agent adapter. It is safe for concurrent use; the
// binary path is resolved once and cached under binaryMu.
type Plugin struct {
	binaryMu       sync.Mutex
	resolvedBinary string
}

// New returns a ready-to-register Crush adapter.
func New() *Plugin {
	return &Plugin{}
}

var _ adapters.Adapter = (*Plugin)(nil)
var _ ports.Agent = (*Plugin)(nil)

// Manifest returns the adapter's static self-description.
func (p *Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{
		ID:          adapterID,
		Name:        "Crush",
		Description: "Run Crush worker sessions.",
		Version:     "0.0.1",
		Capabilities: []adapters.Capability{
			adapters.CapabilityAgent,
		},
	}
}

// GetConfigSpec reports the agent-specific config keys. Crush exposes none yet.
func (p *Plugin) GetConfigSpec(ctx context.Context) (ports.ConfigSpec, error) {
	if err := ctx.Err(); err != nil {
		return ports.ConfigSpec{}, err
	}
	return ports.ConfigSpec{}, nil
}

// GetLaunchCommand builds the argv to start an interactive Crush session.
// Shape:
//
//	crush [--cwd <WorkspacePath>] [--yolo] [-- <Prompt>]
//
// The session runs in the worktree (cwd is set by the runtime). Crush doesn't
// have native system prompt support, so cfg.SystemPrompt / SystemPromptFile are
// intentionally ignored. The initial task prompt is delivered as a positional
// argument after `--`. The --yolo flag corresponds to bypass-permissions mode.
//
// We intentionally do not pass --session on launch: cfg.SessionID is the
// AO-internal id, not a Crush-native session id. Letting Crush mint its own
// native session id (captured by hooks into session metadata) keeps launch
// consistent with GetRestoreCommand, which resumes using that native id.
func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) (cmd []string, err error) {
	binary, err := p.crushBinary(ctx)
	if err != nil {
		return nil, err
	}

	cmd = []string{binary}

	// Crush uses --cwd to set working directory
	if cfg.WorkspacePath != "" {
		cmd = append(cmd, "--cwd", cfg.WorkspacePath)
	}

	// Handle permission modes
	if cfg.Permissions == ports.PermissionModeBypassPermissions {
		cmd = append(cmd, "--yolo")
	}

	// Prompt is passed after `--` so a leading "-" is not read as a flag
	if cfg.Prompt != "" {
		cmd = append(cmd, "--", cfg.Prompt)
	}

	return cmd, nil
}

// GetPromptDeliveryStrategy reports that Crush receives its prompt in the
// launch command itself as a positional argument.
func (p *Plugin) GetPromptDeliveryStrategy(ctx context.Context, cfg ports.LaunchConfig) (ports.PromptDeliveryStrategy, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return ports.PromptDeliveryInCommand, nil
}

// GetRestoreCommand rebuilds the argv that continues an existing Crush session:
// `crush [--cwd <WorkspacePath>] [--yolo] --session <agentSessionId>`.
// It re-applies the permission flag but not the prompt, which the session
// already carries. ok is false when the native session id is not available.
func (p *Plugin) GetRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) (cmd []string, ok bool, err error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	agentSessionID := strings.TrimSpace(cfg.Session.Metadata[ports.MetadataKeyAgentSessionID])
	if agentSessionID == "" {
		return nil, false, nil
	}

	binary, err := p.crushBinary(ctx)
	if err != nil {
		return nil, false, err
	}

	cmd = []string{binary}

	if cfg.Session.WorkspacePath != "" {
		cmd = append(cmd, "--cwd", cfg.Session.WorkspacePath)
	}

	if cfg.Permissions == ports.PermissionModeBypassPermissions {
		cmd = append(cmd, "--yolo")
	}

	cmd = append(cmd, "--session", agentSessionID)
	return cmd, true, nil
}

// SessionInfo surfaces Crush session metadata. Currently a no-op since Crush
// doesn't have full hooks support like Claude Code and Codex. Returns false
// to indicate no metadata is available.
func (p *Plugin) SessionInfo(ctx context.Context, session ports.SessionRef) (ports.SessionInfo, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.SessionInfo{}, false, err
	}
	// No-op for now since Crush doesn't have full hooks support
	return ports.SessionInfo{}, false, nil
}

// ResolveCrushBinary returns the path to the crush binary on this machine,
// searching PATH then a handful of well-known install locations.
// Returns "crush" as a last-ditch fallback.
func ResolveCrushBinary(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	if runtime.GOOS == "windows" {
		for _, name := range []string{"crush.cmd", "crush.exe", "crush"} {
			path, err := exec.LookPath(name)
			if err == nil && path != "" {
				return path, nil
			}
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}

		candidates := []string{}
		if appData := os.Getenv("APPDATA"); appData != "" {
			candidates = append(candidates,
				filepath.Join(appData, "npm", "crush.cmd"),
				filepath.Join(appData, "npm", "crush.exe"),
			)
		}
		if home, err := os.UserHomeDir(); err == nil {
			candidates = append(candidates, filepath.Join(home, ".cargo", "bin", "crush.exe"))
		}
		for _, candidate := range candidates {
			if fileExists(candidate) {
				return candidate, nil
			}
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}

		return "crush", nil
	}

	if path, err := exec.LookPath("crush"); err == nil && path != "" {
		return path, nil
	}

	candidates := []string{
		"/usr/local/bin/crush",
		"/opt/homebrew/bin/crush",
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates,
			filepath.Join(home, ".local", "bin", "crush"),
			filepath.Join(home, ".cargo", "bin", "crush"),
			filepath.Join(home, ".npm", "bin", "crush"),
		)
	}
	for _, candidate := range candidates {
		if fileExists(candidate) {
			return candidate, nil
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
	}

	return "crush", nil
}

func (p *Plugin) crushBinary(ctx context.Context) (string, error) {
	p.binaryMu.Lock()
	defer p.binaryMu.Unlock()

	if p.resolvedBinary != "" {
		return p.resolvedBinary, nil
	}

	binary, err := ResolveCrushBinary(ctx)
	if err != nil {
		return "", err
	}
	p.resolvedBinary = binary
	return binary, nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
