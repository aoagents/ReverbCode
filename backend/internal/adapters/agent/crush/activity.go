package crush

import "github.com/aoagents/agent-orchestrator/backend/internal/domain"

// DeriveActivityState maps a Crush hook event onto an AO activity state.
// Currently a no-op since Crush doesn't have full hooks support like Claude Code and Codex.
// The bool is false to indicate no activity signal is available.
//
// TODO(crush): Implement activity state mapping once Crush has native hook support.
// Until then, runtime exit falls back to the reaper.
func DeriveActivityState(event string, _ []byte) (domain.ActivityState, bool) {
	// No-op for now since Crush doesn't have full hooks support
	return "", false
}
