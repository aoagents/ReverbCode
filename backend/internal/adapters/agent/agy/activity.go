package agy

import (
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// DeriveActivityState maps an Agy hook event onto an AO activity state. The
// bool is false when the event carries no activity signal.
//
// event is the AO hook sub-command name installed in agyManagedHooks:
// "session-start", "session-end", "before-agent", "after-agent", "after-tool".
func DeriveActivityState(event string, _ []byte) (domain.ActivityState, bool) {
	switch event {
	case "before-agent":
		return domain.ActivityActive, true
	case "after-agent":
		return domain.ActivityIdle, true
	case "after-tool":
		return domain.ActivityActive, true
	case "session-end":
		return domain.ActivityExited, true
	case "session-start":
		return "", false
	default:
		return "", false
	}
}
