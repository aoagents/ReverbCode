// Package kimi implements the Kimi CLI (Moonshot AI) agent adapter: launching
// new non-interactive sessions and resuming sessions when a native Kimi session
// id is known.
//
// Kimi CLI (binary "kimi") is Moonshot AI's terminal-native agentic coding
// agent. A new task is run non-interactively with `kimi -p <prompt>`, which
// streams the assistant output to stdout without opening the TUI. Sessions are
// resumed by id with `kimi --session <id>`.
//
// Kimi exposes no native lifecycle/hook system and is not documented as
// Claude Code hook-compatible, so this is a Tier C adapter: hook installation
// and SessionInfo are intentionally no-ops, and activity is left to the
// lifecycle reaper. There is also no documented system-prompt flag, so AO's
// system prompt is not injected. Both should be upgraded if/when Kimi adds the
// corresponding CLI surface.
package kimi

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

const adapterID = "kimi"

// Plugin is the Kimi CLI agent adapter. It is safe for concurrent use; the
// binary path is resolved once and cached under binaryMu.
type Plugin struct {
	binaryMu       sync.Mutex
	resolvedBinary string
}

// New returns a ready-to-register Kimi adapter.
func New() *Plugin {
	return &Plugin{}
}

var _ adapters.Adapter = (*Plugin)(nil)
var _ ports.Agent = (*Plugin)(nil)

// Manifest returns the adapter's static self-description.
func (p *Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{
		ID:          adapterID,
		Name:        "Kimi",
		Description: "Run Kimi CLI (Moonshot AI) worker sessions.",
		Version:     "0.0.1",
		Capabilities: []adapters.Capability{
			adapters.CapabilityAgent,
		},
	}
}

// GetConfigSpec reports no agent-specific config keys yet.
func (p *Plugin) GetConfigSpec(ctx context.Context) (ports.ConfigSpec, error) {
	if err := ctx.Err(); err != nil {
		return ports.ConfigSpec{}, err
	}
	return ports.ConfigSpec{}, nil
}

// GetLaunchCommand builds the argv to start a new non-interactive Kimi session:
//
//	kimi [-y|--auto] -p <prompt>
//
// The prompt is delivered via `-p` (in command), which runs a single prompt
// without opening the TUI. Kimi has no documented system-prompt flag, so
// cfg.SystemPrompt / cfg.SystemPromptFile are not injected.
func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) (cmd []string, err error) {
	binary, err := p.kimiBinary(ctx)
	if err != nil {
		return nil, err
	}

	cmd = []string{binary}
	appendApprovalFlags(&cmd, cfg.Permissions)

	if cfg.Prompt != "" {
		cmd = append(cmd, "-p", cfg.Prompt)
	}

	return cmd, nil
}

// GetPromptDeliveryStrategy reports that Kimi receives its prompt in the launch
// command itself.
func (p *Plugin) GetPromptDeliveryStrategy(ctx context.Context, cfg ports.LaunchConfig) (ports.PromptDeliveryStrategy, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return ports.PromptDeliveryInCommand, nil
}

// GetAgentHooks is intentionally a no-op: Kimi CLI exposes no native hook system
// and is not documented as Claude Code hook-compatible.
func (p *Plugin) GetAgentHooks(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	return ctx.Err()
}

// GetRestoreCommand rebuilds the argv that continues an existing Kimi session
// when a native Kimi session id is known:
//
//	kimi [-y|--auto] --session <agentSessionId>
//
// ok is false when no native session id has been captured, so callers fall back
// to fresh launch behavior. Kimi has no lifecycle hook for AO to capture the
// native session id from yet, so in practice this returns ok=false today.
func (p *Plugin) GetRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) (cmd []string, ok bool, err error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	agentSessionID := strings.TrimSpace(cfg.Session.Metadata[ports.MetadataKeyAgentSessionID])
	if agentSessionID == "" {
		return nil, false, nil
	}

	binary, err := p.kimiBinary(ctx)
	if err != nil {
		return nil, false, err
	}
	cmd = make([]string, 0, 4)
	cmd = append(cmd, binary)
	appendApprovalFlags(&cmd, cfg.Permissions)
	cmd = append(cmd, "--session", agentSessionID)
	return cmd, true, nil
}

// SessionInfo is intentionally a no-op until Kimi exposes a way to capture its
// native session id and display metadata.
func (p *Plugin) SessionInfo(ctx context.Context, session ports.SessionRef) (ports.SessionInfo, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.SessionInfo{}, false, err
	}
	return ports.SessionInfo{}, false, nil
}

// appendApprovalFlags maps AO's permission modes onto Kimi's approval flags.
//
//   - Default: no flag, deferring to the user's Kimi config/default behavior.
//   - AcceptEdits / Auto: `--auto` (auto permission mode; approvals handled
//     automatically).
//   - BypassPermissions: `-y` (yolo; auto-approve regular tool calls including
//     file writes and shell execution).
func appendApprovalFlags(cmd *[]string, permissions ports.PermissionMode) {
	switch normalizePermissionMode(permissions) {
	case ports.PermissionModeDefault:
		// No flag: defer to the user's Kimi config/default behavior.
	case ports.PermissionModeAcceptEdits, ports.PermissionModeAuto:
		*cmd = append(*cmd, "--auto")
	case ports.PermissionModeBypassPermissions:
		*cmd = append(*cmd, "-y")
	}
}

func normalizePermissionMode(mode ports.PermissionMode) ports.PermissionMode {
	switch mode {
	case ports.PermissionModeDefault,
		ports.PermissionModeAcceptEdits,
		ports.PermissionModeAuto,
		ports.PermissionModeBypassPermissions:
		return mode
	default:
		return ports.PermissionModeDefault
	}
}

// ResolveKimiBinary finds the `kimi` binary, searching PATH then common install
// locations (the uv tool/curl installer drops it in ~/.local/bin, plus Homebrew
// and ~/.cargo/bin). It returns "kimi" as a last resort so callers get the
// shell's normal command-not-found behavior if Kimi is absent.
func ResolveKimiBinary(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	if runtime.GOOS == "windows" {
		for _, name := range []string{"kimi.cmd", "kimi.exe", "kimi"} {
			if path, err := exec.LookPath(name); err == nil && path != "" {
				return path, nil
			}
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		candidates := []string{}
		if appData := os.Getenv("APPDATA"); appData != "" {
			candidates = append(candidates,
				filepath.Join(appData, "npm", "kimi.cmd"),
				filepath.Join(appData, "npm", "kimi.exe"),
			)
		}
		if home, err := os.UserHomeDir(); err == nil {
			candidates = append(candidates,
				filepath.Join(home, ".local", "bin", "kimi.exe"),
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
		return "kimi", nil
	}

	if path, err := exec.LookPath("kimi"); err == nil && path != "" {
		return path, nil
	}

	candidates := []string{
		"/usr/local/bin/kimi",
		"/opt/homebrew/bin/kimi",
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates,
			filepath.Join(home, ".local", "bin", "kimi"),
			filepath.Join(home, ".cargo", "bin", "kimi"),
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

	return "kimi", nil
}

func (p *Plugin) kimiBinary(ctx context.Context) (string, error) {
	p.binaryMu.Lock()
	defer p.binaryMu.Unlock()

	if p.resolvedBinary != "" {
		return p.resolvedBinary, nil
	}

	binary, err := ResolveKimiBinary(ctx)
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
