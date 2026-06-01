// Package codex implements the Codex agent adapter: launching new sessions,
// resuming hook-tracked sessions, installing workspace-local hooks, and reading
// hook-derived session info.
//
// Better-AO-managed sessions derive native session identity and display
// metadata from Codex hooks instead of transcript/cache scans.
package codex

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent"
)

const (
	codexAgentSessionIDMetadataKey = "agentSessionId"
	codexTitleMetadataKey          = "title"
	codexSummaryMetadataKey        = "summary"
)

type Plugin struct {
	binaryMu       sync.Mutex
	resolvedBinary string
}

func New() *Plugin {
	return &Plugin{}
}

var _ adapters.Adapter = (*Plugin)(nil)
var _ agent.Agent = (*Plugin)(nil)

func (p *Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{
		ID:          "codex",
		Name:        "Codex",
		Description: "Run Codex worker sessions.",
		Version:     "0.0.1",
		Capabilities: []adapters.Capability{
			adapters.CapabilityAgent,
		},
	}
}

func (p *Plugin) GetConfigSpec(ctx context.Context) (agent.ConfigSpec, error) {
	if err := ctx.Err(); err != nil {
		return agent.ConfigSpec{}, err
	}
	return agent.ConfigSpec{}, nil
}

func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg agent.LaunchConfig) (cmd []string, err error) {
	binary, err := p.codexBinary(ctx)
	if err != nil {
		return nil, err
	}

	cmd = []string{binary}
	appendNoUpdateCheckFlag(&cmd)
	appendApprovalFlags(&cmd, cfg.Permissions)

	if cfg.SystemPromptFile != "" {
		cmd = append(cmd, "-c", "model_instructions_file="+cfg.SystemPromptFile)
	} else if cfg.SystemPrompt != "" {
		cmd = append(cmd, "-c", "developer_instructions="+cfg.SystemPrompt)
	}

	if cfg.Prompt != "" {
		cmd = append(cmd, "--", cfg.Prompt)
	}

	return cmd, nil
}

func (p *Plugin) GetPromptDeliveryStrategy(ctx context.Context, cfg agent.LaunchConfig) (agent.PromptDeliveryStrategy, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return agent.PromptDeliveryInCommand, nil
}

// GetRestoreCommand rebuilds the argv that continues an existing Codex
// session: `codex resume <agentSessionId>`. ok is false when the hook-derived
// native session id has not landed yet, so callers can fall back to fresh
// launch behavior.
func (p *Plugin) GetRestoreCommand(ctx context.Context, cfg agent.RestoreConfig) (cmd []string, ok bool, err error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	agentSessionID := strings.TrimSpace(cfg.Session.Metadata[codexAgentSessionIDMetadataKey])
	if agentSessionID == "" {
		return nil, false, nil
	}

	binary, err := p.codexBinary(ctx)
	if err != nil {
		return nil, false, err
	}

	cmd = []string{binary, "resume"}
	appendNoUpdateCheckFlag(&cmd)
	appendApprovalFlags(&cmd, cfg.Permissions)
	cmd = append(cmd, agentSessionID)
	return cmd, true, nil
}

// SessionInfo surfaces Codex hook-derived metadata. Metadata is intentionally
// nil for Codex: callers get the normalized fields directly.
func (p *Plugin) SessionInfo(ctx context.Context, session agent.SessionRef) (agent.SessionInfo, bool, error) {
	if err := ctx.Err(); err != nil {
		return agent.SessionInfo{}, false, err
	}
	info := agent.SessionInfo{
		AgentSessionID: session.Metadata[codexAgentSessionIDMetadataKey],
		Title:          session.Metadata[codexTitleMetadataKey],
		Summary:        session.Metadata[codexSummaryMetadataKey],
	}
	if info.AgentSessionID == "" && info.Title == "" && info.Summary == "" {
		return agent.SessionInfo{}, false, nil
	}
	return info, true, nil
}

// ResolveCodexBinary returns the path to the codex binary on this machine,
// searching PATH then a handful of well-known install locations
// (Homebrew, Cargo, npm global). Returns "codex" as a last-ditch fallback
// so callers see a clear "command not found" rather than an empty argv.
func ResolveCodexBinary(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	if runtime.GOOS == "windows" {
		for _, name := range []string{"codex.cmd", "codex.exe", "codex"} {
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
				filepath.Join(appData, "npm", "codex.cmd"),
				filepath.Join(appData, "npm", "codex.exe"),
			)
		}
		if home, err := os.UserHomeDir(); err == nil {
			candidates = append(candidates, filepath.Join(home, ".cargo", "bin", "codex.exe"))
		}
		for _, candidate := range candidates {
			if fileExists(candidate) {
				return candidate, nil
			}
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}

		return "codex", nil
	}

	if path, err := exec.LookPath("codex"); err == nil && path != "" {
		return path, nil
	}

	candidates := []string{
		"/usr/local/bin/codex",
		"/opt/homebrew/bin/codex",
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates,
			filepath.Join(home, ".cargo", "bin", "codex"),
			filepath.Join(home, ".npm", "bin", "codex"),
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

	return "codex", nil
}

func (p *Plugin) codexBinary(ctx context.Context) (string, error) {
	p.binaryMu.Lock()
	defer p.binaryMu.Unlock()

	if p.resolvedBinary != "" {
		return p.resolvedBinary, nil
	}

	binary, err := ResolveCodexBinary(ctx)
	if err != nil {
		return "", err
	}
	p.resolvedBinary = binary
	return binary, nil
}

func appendNoUpdateCheckFlag(cmd *[]string) {
	*cmd = append(*cmd, "-c", "check_for_update_on_startup=false")
}

func appendApprovalFlags(cmd *[]string, permissions agent.PermissionMode) {
	switch normalizePermissionMode(permissions) {
	case agent.PermissionModeDefault:
		// No flag: defer to the user's Codex config/default behavior.
	case agent.PermissionModeAcceptEdits:
		*cmd = append(*cmd, "--ask-for-approval", "on-request")
	case agent.PermissionModeAuto:
		*cmd = append(*cmd, "--ask-for-approval", "on-request", "-c", `approvals_reviewer="auto_review"`)
	case agent.PermissionModeBypassPermissions:
		*cmd = append(*cmd, "--dangerously-bypass-approvals-and-sandbox")
	}
}

func normalizePermissionMode(mode agent.PermissionMode) agent.PermissionMode {
	switch mode {
	case agent.PermissionModeDefault,
		agent.PermissionModeAcceptEdits,
		agent.PermissionModeAuto,
		agent.PermissionModeBypassPermissions:
		return mode
	default:
		return agent.PermissionModeDefault
	}
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
