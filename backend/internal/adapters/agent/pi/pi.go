// Package pi implements the Pi agent adapter: launching new headless Pi
// sessions and resuming sessions when a native Pi session id is known.
//
// Pi (badlogic / "@earendil-works/pi-coding-agent", binary "pi") is a minimal
// terminal coding harness. AO drives it non-interactively with `-p` / `--print`
// ("process prompt and exit"). The initial prompt is delivered in-command as a
// trailing positional message; Pi's argument parser does not honor a `--`
// options terminator, so AO relies on prompts not beginning with a literal "-".
//
// System prompts are appended to Pi's default coding-assistant prompt via
// `--append-system-prompt <text>`. Pi's flag takes inline text only (no file
// variant), so a system-prompt file is read from disk and its contents are
// inlined into the flag; a read failure aborts the launch.
//
// Permissions: Pi has no permission/approval CLI flags ("No permission popups" —
// confirmation flows are built via TypeScript extensions), so AO emits no
// permission flag and defers to Pi's own behavior.
//
// Restore: Pi persists sessions to ~/.pi/agent/sessions/ and resumes by id with
// `--session <id>` (partial UUIDs accepted). The native session id is emitted on
// the first line of `--mode json` output as {"type":"session","id":"<uuid>",...}
// and is captured into session metadata out-of-band; GetRestoreCommand reads it
// back from metadata. ok=false when no native id is known (manager falls back to
// a fresh launch).
//
// Hooks/activity: Pi exposes lifecycle hooks only through in-process TypeScript
// extensions (pi.on("session_start", ...), etc.), not a config file AO can
// install, and it has no Claude Code hook compatibility. There is therefore no
// Tier A native hook installer nor a Tier B Claude-compat delegation; hook
// installation and SessionInfo are intentionally no-ops until a Pi-specific
// extension exists.
package pi

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

const adapterID = "pi"

// Plugin is the Pi agent adapter. It is safe for concurrent use; the binary
// path is resolved once and cached under binaryMu.
type Plugin struct {
	binaryMu       sync.Mutex
	resolvedBinary string
}

// New returns a ready-to-register Pi adapter.
func New() *Plugin {
	return &Plugin{}
}

var _ adapters.Adapter = (*Plugin)(nil)
var _ ports.Agent = (*Plugin)(nil)

// Manifest returns the adapter's static self-description.
func (p *Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{
		ID:          adapterID,
		Name:        "Pi",
		Description: "Run Pi worker sessions.",
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

// GetLaunchCommand builds the argv to start a new headless Pi session:
//
//	pi --print [--append-system-prompt <system prompt>] [<prompt>]
//
// The prompt is delivered in-command as a trailing positional message. Pi does
// not honor a `--` options terminator, so the prompt must not begin with "-".
// Pi has no permission flags, so none are emitted.
func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) (cmd []string, err error) {
	binary, err := p.piBinary(ctx)
	if err != nil {
		return nil, err
	}

	cmd = []string{binary, "--print"}
	if cfg.SystemPromptFile != "" {
		data, err := os.ReadFile(cfg.SystemPromptFile) //nolint:gosec // path is AO-owned launch config
		if err != nil {
			return nil, err
		}
		cmd = append(cmd, "--append-system-prompt", string(data))
	} else if cfg.SystemPrompt != "" {
		cmd = append(cmd, "--append-system-prompt", cfg.SystemPrompt)
	}
	if cfg.Prompt != "" {
		cmd = append(cmd, cfg.Prompt)
	}
	return cmd, nil
}

// GetPromptDeliveryStrategy reports that Pi receives its prompt in the launch
// command itself.
func (p *Plugin) GetPromptDeliveryStrategy(ctx context.Context, cfg ports.LaunchConfig) (ports.PromptDeliveryStrategy, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return ports.PromptDeliveryInCommand, nil
}

// GetAgentHooks is intentionally a no-op: Pi's lifecycle hooks are only
// reachable through in-process TypeScript extensions, not a config file AO can
// install, and Pi has no Claude Code hook compatibility.
func (p *Plugin) GetAgentHooks(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	return ctx.Err()
}

// GetRestoreCommand rebuilds the argv that continues an existing Pi session when
// a native session id is available in metadata. Pi resumes by id with
// `--session <id>` (partial UUIDs accepted). Until that id exists, ok is false
// and callers fall back to fresh launch behavior.
func (p *Plugin) GetRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) (cmd []string, ok bool, err error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	agentSessionID := strings.TrimSpace(cfg.Session.Metadata[ports.MetadataKeyAgentSessionID])
	if agentSessionID == "" {
		return nil, false, nil
	}

	binary, err := p.piBinary(ctx)
	if err != nil {
		return nil, false, err
	}
	cmd = []string{binary, "--print", "--session", agentSessionID}
	return cmd, true, nil
}

// SessionInfo is intentionally a no-op until a Pi-specific extension persists
// session metadata (title/summary). The native session id, when known, is read
// directly from metadata by GetRestoreCommand.
func (p *Plugin) SessionInfo(ctx context.Context, session ports.SessionRef) (ports.SessionInfo, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.SessionInfo{}, false, err
	}
	return ports.SessionInfo{}, false, nil
}

// ResolvePiBinary finds the `pi` binary, searching PATH then common install
// locations. It returns "pi" as a last resort so callers get the shell's normal
// command-not-found behavior if Pi is absent.
func ResolvePiBinary(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	if runtime.GOOS == "windows" {
		for _, name := range []string{"pi.cmd", "pi.exe", "pi"} {
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
				filepath.Join(appData, "npm", "pi.cmd"),
				filepath.Join(appData, "npm", "pi.exe"),
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
		return "pi", nil
	}

	if path, err := exec.LookPath("pi"); err == nil && path != "" {
		return path, nil
	}

	candidates := []string{
		"/usr/local/bin/pi",
		"/opt/homebrew/bin/pi",
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates,
			filepath.Join(home, ".npm-global", "bin", "pi"),
			filepath.Join(home, ".local", "bin", "pi"),
			filepath.Join(home, ".pi", "bin", "pi"),
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

	return "pi", nil
}

func (p *Plugin) piBinary(ctx context.Context) (string, error) {
	p.binaryMu.Lock()
	defer p.binaryMu.Unlock()

	if p.resolvedBinary != "" {
		return p.resolvedBinary, nil
	}

	binary, err := ResolvePiBinary(ctx)
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
