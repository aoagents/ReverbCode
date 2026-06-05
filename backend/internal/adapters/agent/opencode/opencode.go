// Package opencode implements the opencode (sst/opencode) agent adapter:
// launching new TUI sessions, resuming sessions by native id, installing a
// workspace-local activity plugin, and reading plugin-derived session info.
//
// opencode differs from Claude Code and Codex in two ways AO has to bridge:
//   - It has no native command-hook config (no settings.local.json / hooks.json
//     equivalent). Its only lifecycle-extensibility surface is a JS/TS plugin
//     loaded from .opencode/plugins/, so GetAgentHooks installs an AO-owned
//     plugin file (see hooks.go) instead of merging JSON.
//   - Its interactive TUI exposes no permission flag
//     (--dangerously-skip-permissions lives only on `opencode run`, not the
//     default TUI command AO launches) and no system-prompt flag. AO's graduated
//     permission modes are delivered via the OPENCODE_PERMISSION env var (see
//     opencodePermissionConfig); the system prompt defers to opencode's own config.
//
// AO-managed sessions derive native session identity and display metadata from
// the opencode plugin's reported events, mirroring the Codex adapter.
package opencode

import (
	"context"
	"encoding/json"
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
	// `ao spawn --agent`. It matches domain.HarnessOpenCode.
	adapterID = "opencode"

	// Normalized session-metadata keys the opencode plugin persists into the AO
	// session store and SessionInfo reads back. Shared vocabulary with the Codex
	// and Claude Code adapters so the dashboard treats every agent uniformly.
	opencodeAgentSessionIDMetadataKey = "agentSessionId"
	opencodeTitleMetadataKey          = "title"
	opencodeSummaryMetadataKey        = "summary"
)

// Plugin is the opencode agent adapter. It is safe for concurrent use; the
// binary path is resolved once and cached under binaryMu.
type Plugin struct {
	binaryMu       sync.Mutex
	resolvedBinary string
}

// New returns a ready-to-register opencode adapter.
func New() *Plugin {
	return &Plugin{}
}

var _ adapters.Adapter = (*Plugin)(nil)
var _ ports.Agent = (*Plugin)(nil)

// Manifest returns the adapter's static self-description.
func (p *Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{
		ID:          adapterID,
		Name:        "opencode",
		Description: "Run opencode worker sessions.",
		Version:     "0.0.1",
		Capabilities: []adapters.Capability{
			adapters.CapabilityAgent,
		},
	}
}

// GetConfigSpec reports the agent-specific config keys. opencode exposes none
// yet: model and agent selection are read from opencode's own config
// (opencode.json / ~/.config/opencode), exactly as a normal launch.
func (p *Plugin) GetConfigSpec(ctx context.Context) (ports.ConfigSpec, error) {
	if err := ctx.Err(); err != nil {
		return ports.ConfigSpec{}, err
	}
	return ports.ConfigSpec{}, nil
}

// GetLaunchCommand builds the argv to start a new interactive opencode session.
// Shape:
//
//	[env OPENCODE_PERMISSION=<json>] opencode [--prompt <prompt>]
//
// The session runs in the worktree (cwd is set by the runtime, as for Claude
// Code and Codex). opencode has no CLI flag to set a system prompt, so
// cfg.SystemPrompt / SystemPromptFile are intentionally ignored here — opencode
// resolves instructions from its own config and AGENTS.md rules. The initial
// task prompt is delivered via --prompt (its argument, so a leading "-" is not
// read as a flag). Non-default permission modes prepend an `env` assignment
// rather than a flag (see opencodePermissionEnvPrefix).
func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) (cmd []string, err error) {
	binary, err := p.opencodeBinary(ctx)
	if err != nil {
		return nil, err
	}

	cmd = append(opencodePermissionEnvPrefix(cfg.Permissions), binary)
	if cfg.Prompt != "" {
		cmd = append(cmd, "--prompt", cfg.Prompt)
	}
	return cmd, nil
}

// GetPromptDeliveryStrategy reports that opencode receives its prompt in the
// launch command itself (via --prompt).
func (p *Plugin) GetPromptDeliveryStrategy(ctx context.Context, cfg ports.LaunchConfig) (ports.PromptDeliveryStrategy, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return ports.PromptDeliveryInCommand, nil
}

// GetRestoreCommand rebuilds the argv that continues an existing opencode
// session: `[env OPENCODE_PERMISSION=<json>] opencode --session <agentSessionId>`.
// It re-applies the permission env (resume otherwise reverts to the configured
// default) but not the prompt, which the session already carries. ok is false
// when the plugin-derived native session id has not landed yet, so callers fall
// back to fresh launch behavior — mirroring the Codex adapter.
func (p *Plugin) GetRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) (cmd []string, ok bool, err error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	agentSessionID := strings.TrimSpace(cfg.Session.Metadata[opencodeAgentSessionIDMetadataKey])
	if agentSessionID == "" {
		return nil, false, nil
	}

	binary, err := p.opencodeBinary(ctx)
	if err != nil {
		return nil, false, err
	}

	cmd = append(opencodePermissionEnvPrefix(cfg.Permissions), binary, "--session", agentSessionID)
	return cmd, true, nil
}

// SessionInfo surfaces opencode plugin-derived metadata. Metadata is
// intentionally nil for opencode: callers get the normalized fields directly,
// matching the Codex adapter.
func (p *Plugin) SessionInfo(ctx context.Context, session ports.SessionRef) (ports.SessionInfo, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.SessionInfo{}, false, err
	}
	info := ports.SessionInfo{
		AgentSessionID: session.Metadata[opencodeAgentSessionIDMetadataKey],
		Title:          session.Metadata[opencodeTitleMetadataKey],
		Summary:        session.Metadata[opencodeSummaryMetadataKey],
	}
	if info.AgentSessionID == "" && info.Title == "" && info.Summary == "" {
		return ports.SessionInfo{}, false, nil
	}
	return info, true, nil
}

// opencodePermissionEnvVar is the env var opencode merges into its permission
// config (see opencode's config.ts). It is the only permission-control surface
// the interactive TUI honors: the --dangerously-skip-permissions flag exists
// solely on `opencode run`, not on the default TUI command AO launches, so
// passing any permission flag makes opencode reject the argv and the session
// fails to launch.
const opencodePermissionEnvVar = "OPENCODE_PERMISSION"

// opencodePermissionConfig maps an AO permission mode onto opencode's permission
// config (tool -> action). Tools left unset fall back to opencode's own default
// action ("ask"), so each mode only names the tools it relaxes:
//   - default            → nil: no env; opencode's config decides every prompt.
//   - accept-edits       → edits ("write"/"edit"/"patch" all gate on the "edit"
//     key) auto-approved; bash and everything else still prompt.
//   - auto               → edits + bash auto-approved; network/other still prompt.
//     opencode has no classifier/reviewer gate (unlike Claude Code's "auto"), so
//     this is the closest analog its flat allow/ask/deny config can express.
//   - bypass-permissions → "*" wildcard-allows every tool: nothing prompts.
func opencodePermissionConfig(mode ports.PermissionMode) map[string]string {
	switch normalizePermissionMode(mode) {
	case ports.PermissionModeAcceptEdits:
		return map[string]string{"edit": "allow"}
	case ports.PermissionModeAuto:
		return map[string]string{"edit": "allow", "bash": "allow"}
	case ports.PermissionModeBypassPermissions:
		return map[string]string{"*": "allow"}
	default:
		return nil
	}
}

// opencodePermissionEnvPrefix renders mode's permission config as an
// `env OPENCODE_PERMISSION=<json>` argv prefix, or nil for the default mode.
//
// The var must reach opencode as a process env var, not an argv flag. The zellij
// runtime runs the argv through a shell, which execs `env`, which sets the var
// and execs opencode. A bare `OPENCODE_PERMISSION=...` argv element would not
// work: the runtime shell-quotes every element, and a quoted token is run as a
// command rather than read as an assignment — hence the explicit `env` wrapper.
// POSIX-only, which matches the zellij runtime.
func opencodePermissionEnvPrefix(mode ports.PermissionMode) []string {
	config := opencodePermissionConfig(mode)
	if len(config) == 0 {
		return nil
	}
	// Marshaling a map[string]string never errors and emits keys in sorted order,
	// so the prefix is deterministic for tests and reproducible across launches.
	blob, _ := json.Marshal(config)
	return []string{"env", opencodePermissionEnvVar + "=" + string(blob)}
}

func normalizePermissionMode(mode ports.PermissionMode) ports.PermissionMode {
	switch mode {
	case ports.PermissionModeDefault,
		ports.PermissionModeAcceptEdits,
		ports.PermissionModeAuto,
		ports.PermissionModeBypassPermissions:
		return mode
	default:
		// Empty or unrecognized: defer to opencode's own config (no flag).
		return ports.PermissionModeDefault
	}
}

// ResolveOpenCodeBinary returns the path to the opencode binary on this machine,
// searching PATH then a handful of well-known install locations (the install
// script's ~/.opencode/bin, Homebrew, npm global). Returns "opencode" as a
// last-ditch fallback so callers see a clear "command not found" rather than an
// empty argv.
func ResolveOpenCodeBinary(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	if runtime.GOOS == "windows" {
		for _, name := range []string{"opencode.cmd", "opencode.exe", "opencode"} {
			if path, err := exec.LookPath(name); err == nil && path != "" {
				return path, nil
			}
		}
		candidates := []string{}
		if appData := os.Getenv("APPDATA"); appData != "" {
			candidates = append(candidates,
				filepath.Join(appData, "npm", "opencode.cmd"),
				filepath.Join(appData, "npm", "opencode.exe"),
			)
		}
		for _, candidate := range candidates {
			if fileExists(candidate) {
				return candidate, nil
			}
		}
		return "opencode", nil
	}

	if path, err := exec.LookPath("opencode"); err == nil && path != "" {
		return path, nil
	}

	candidates := []string{
		"/usr/local/bin/opencode",
		"/opt/homebrew/bin/opencode",
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates,
			filepath.Join(home, ".opencode", "bin", "opencode"),
			filepath.Join(home, ".npm", "bin", "opencode"),
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

	return "opencode", nil
}

func (p *Plugin) opencodeBinary(ctx context.Context) (string, error) {
	p.binaryMu.Lock()
	defer p.binaryMu.Unlock()

	if p.resolvedBinary != "" {
		return p.resolvedBinary, nil
	}

	binary, err := ResolveOpenCodeBinary(ctx)
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
