// Package auggie implements the Auggie (Augment Code) agent adapter: launching
// new headless Auggie sessions and resuming sessions when a native Auggie
// session id is known.
//
// Auggie is Augment Code's terminal coding agent (binary "auggie", installed via
// `npm install -g @augmentcode/auggie`). It exposes a headless one-shot mode via
// `--print` (alias `-p`) which runs a single instruction and exits — the mode AO
// uses to drive it unattended.
//
// Launch shape:
//
//	auggie --print [--instruction-file <f> | --instruction <s>] [-- <prompt>]
//
// The prompt is the print-mode positional, passed after `--` so a prompt
// beginning with "-" is not mistaken for a flag. A system prompt, when supplied,
// is injected via Auggie's `--instruction-file` / `--instruction` flags, which
// append guidance to the workspace rules.
//
// Permissions: Auggie has no single "approve everything" flag. It governs
// unattended tool/file approval through granular `--permission <tool>:<allow|deny>`
// rules (and a read-only `--ask` mode), not a 4-mode bypass like Claude Code.
// Because there is no verifiable blanket auto-approve flag, every AO permission
// mode emits no flag and defers to the user's Auggie configuration, rather than
// guessing a flag that does not exist.
//
// Resume: Auggie supports `--resume <sessionId>` (alias `-r`), usable with
// `--print` for headless resume. AO only has a native session id to resume from
// when one was captured into session metadata; Auggie exposes no hook/lifecycle
// system, so that id is not captured automatically yet. GetRestoreCommand
// therefore returns ok=false until a native session id is present, at which point
// callers fall back to a fresh launch.
//
// Hooks/activity: Auggie has no hook or lifecycle event system (it reads
// .claude/commands/ for slash commands, but that is not Claude Code hook
// compatibility). Hook installation and SessionInfo are intentionally no-ops
// (Tier C) until an Auggie-specific activity integration exists.
package auggie

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

const adapterID = "auggie"

// Plugin is the Auggie agent adapter. It is safe for concurrent use; the binary
// path is resolved once and cached under binaryMu.
type Plugin struct {
	binaryMu       sync.Mutex
	resolvedBinary string
}

// New returns a ready-to-register Auggie adapter.
func New() *Plugin {
	return &Plugin{}
}

var _ adapters.Adapter = (*Plugin)(nil)
var _ ports.Agent = (*Plugin)(nil)

// Manifest returns the adapter's static self-description.
func (p *Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{
		ID:          adapterID,
		Name:        "Auggie",
		Description: "Run Auggie (Augment Code) worker sessions.",
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

// GetLaunchCommand builds the argv to start a new headless Auggie session:
//
//	auggie --print [--instruction-file <f> | --instruction <s>] [-- <prompt>]
//
// The prompt is passed after `--` so a prompt beginning with "-" is not mistaken
// for a flag. A system prompt is injected via --instruction-file / --instruction,
// mirroring the system-prompt handling of the other adapters.
func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) (cmd []string, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	binary, err := p.auggieBinary(ctx)
	if err != nil {
		return nil, err
	}

	cmd = []string{binary, "--print"}
	if cfg.SystemPromptFile != "" {
		cmd = append(cmd, "--instruction-file", cfg.SystemPromptFile)
	} else if cfg.SystemPrompt != "" {
		cmd = append(cmd, "--instruction", cfg.SystemPrompt)
	}
	if cfg.Prompt != "" {
		cmd = append(cmd, "--", cfg.Prompt)
	}
	return cmd, nil
}

// GetPromptDeliveryStrategy reports that Auggie receives its prompt in the launch
// command itself (the print-mode positional).
func (p *Plugin) GetPromptDeliveryStrategy(ctx context.Context, cfg ports.LaunchConfig) (ports.PromptDeliveryStrategy, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return ports.PromptDeliveryInCommand, nil
}

// GetAgentHooks is intentionally a no-op: Auggie has no hook or lifecycle event
// system, so there is nothing to install. Activity reporting will require an
// Auggie-specific integration once one exists.
func (p *Plugin) GetAgentHooks(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	return ctx.Err()
}

// GetRestoreCommand rebuilds the argv that continues an existing Auggie session
// when a native session id is available in metadata:
//
//	auggie --print --resume <sessionId>
//
// Auggie has no hook surface to capture that id automatically yet, so in practice
// the id is empty and ok is false, letting callers fall back to a fresh launch.
func (p *Plugin) GetRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) (cmd []string, ok bool, err error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	agentSessionID := strings.TrimSpace(cfg.Session.Metadata[ports.MetadataKeyAgentSessionID])
	if agentSessionID == "" {
		return nil, false, nil
	}

	binary, err := p.auggieBinary(ctx)
	if err != nil {
		return nil, false, err
	}
	cmd = []string{binary, "--print", "--resume", agentSessionID}
	return cmd, true, nil
}

// SessionInfo is intentionally a no-op until Auggie session metadata can be
// captured (Auggie exposes no hook surface to derive it from).
func (p *Plugin) SessionInfo(ctx context.Context, session ports.SessionRef) (ports.SessionInfo, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.SessionInfo{}, false, err
	}
	return ports.SessionInfo{}, false, nil
}

// Auggie has no single blanket auto-approve/bypass flag; unattended tool/file
// approval is governed by granular `--permission <tool>:<allow|deny>` rules, so
// AO emits no approval flag and defers every mode to the user's Auggie config.
// There is therefore no appendApprovalFlags helper for this adapter.

// ResolveAuggieBinary finds the `auggie` binary, searching PATH then common
// install locations. It returns "auggie" as a last resort so callers get the
// shell's normal command-not-found behavior if Auggie is absent.
func ResolveAuggieBinary(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	if runtime.GOOS == "windows" {
		for _, name := range []string{"auggie.cmd", "auggie.exe", "auggie"} {
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
				filepath.Join(appData, "npm", "auggie.cmd"),
				filepath.Join(appData, "npm", "auggie.exe"),
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
		return "auggie", nil
	}

	if path, err := exec.LookPath("auggie"); err == nil && path != "" {
		return path, nil
	}

	candidates := []string{
		"/usr/local/bin/auggie",
		"/opt/homebrew/bin/auggie",
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates,
			filepath.Join(home, ".local", "bin", "auggie"),
			filepath.Join(home, ".npm", "bin", "auggie"),
			filepath.Join(home, ".npm-global", "bin", "auggie"),
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

	return "auggie", nil
}

func (p *Plugin) auggieBinary(ctx context.Context) (string, error) {
	p.binaryMu.Lock()
	defer p.binaryMu.Unlock()

	if p.resolvedBinary != "" {
		return p.resolvedBinary, nil
	}

	binary, err := ResolveAuggieBinary(ctx)
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
