package codex

import "github.com/aoagents/agent-orchestrator/backend/internal/domain"

// DeriveActivityState maps a Codex hook event onto an AO activity state. The
// bool is false when the event carries no activity signal.
//
// event is the AO hook sub-command name installed in codexManagedHooks
// ("user-prompt-submit", "permission-request", "stop", ...), not the native
// Codex event name. Codex currently has no SessionEnd/Notification equivalent
// in the adapter, so runtime exit still falls back to the reaper.
//
// TODO(codex): ActivityExited is still runtime-observation-owned. If Codex adds
// a native session/process-end hook, map that hook to ActivityExited here. Until
// then, make sure the lifecycle reaper can still mark a dead Codex runtime as
// exited even when the last hook signal was sticky waiting_input.
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
