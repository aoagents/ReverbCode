package codex

import "github.com/aoagents/agent-orchestrator/backend/internal/domain"

// DeriveActivityState maps a Codex hook event onto an AO activity state. The
// bool is false when the event carries no activity signal.
func DeriveActivityState(event string, _ []byte) (domain.ActivityState, bool) {
	switch event {
	case "user-prompt-submit":
		return domain.ActivityActive, true
	case "permission-request":
		return domain.ActivityWaitingInput, true
	case "stop":
		return domain.ActivityIdle, true
	default:
		return "", false
	}
}
