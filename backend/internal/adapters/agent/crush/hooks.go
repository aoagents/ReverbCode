package crush

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// GetAgentHooks is a no-op for Crush since it doesn't have full hooks support
// like Claude Code and Codex. Crush doesn't have a native hook configuration system
// that AO can integrate with for session metadata tracking.
//
// TODO(crush): Implement hook installation once Crush has native hook support.
// Until then, session metadata tracking is not available.
func (p *Plugin) GetAgentHooks(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// No-op for now since Crush doesn't have full hooks support
	return nil
}

// UninstallHooks is a no-op for Crush.
func (p *Plugin) UninstallHooks(ctx context.Context, workspacePath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// No-op for now since Crush doesn't have full hooks support
	return nil
}

// AreHooksInstalled is a no-op for Crush.
func (p *Plugin) AreHooksInstalled(ctx context.Context, workspacePath string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	// No-op for now since Crush doesn't have full hooks support
	return false, nil
}
