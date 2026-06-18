package domain

import "fmt"

// AgentDefaults are the app-wide fallback harnesses used when a spawn does not
// name an explicit harness and the project has no role override.
type AgentDefaults struct {
	DefaultWorkerAgent       AgentHarness `json:"defaultWorkerAgent,omitempty" enum:"claude-code,codex,aider,opencode,grok,droid,amp,agy,crush,cursor,qwen,copilot,goose,auggie,continue,devin,cline,kimi,kiro,kilocode,vibe,pi,autohand"`
	DefaultOrchestratorAgent AgentHarness `json:"defaultOrchestratorAgent,omitempty" enum:"claude-code,codex,aider,opencode,grok,droid,amp,agy,crush,cursor,qwen,copilot,goose,auggie,continue,devin,cline,kimi,kiro,kilocode,vibe,pi,autohand"`
}

// HarnessFor returns the default harness for a session kind. Any non-worker
// role is treated as an orchestrator role because the domain only has those two
// concrete spawn roles today.
func (d AgentDefaults) HarnessFor(kind SessionKind) AgentHarness {
	if kind == KindWorker || kind == "" {
		return d.DefaultWorkerAgent
	}
	return d.DefaultOrchestratorAgent
}

// Complete reports whether both app-wide defaults have been configured.
func (d AgentDefaults) Complete() bool {
	return d.DefaultWorkerAgent != "" && d.DefaultOrchestratorAgent != ""
}

// ValidateComplete rejects missing or unknown defaults. Settings writes use
// this strict path so the app never persists a half-configured default state.
func (d AgentDefaults) ValidateComplete() error {
	if d.DefaultWorkerAgent == "" {
		return fmt.Errorf("defaultWorkerAgent is required")
	}
	if !d.DefaultWorkerAgent.IsKnown() {
		return fmt.Errorf("defaultWorkerAgent: unknown harness %q", d.DefaultWorkerAgent)
	}
	if d.DefaultOrchestratorAgent == "" {
		return fmt.Errorf("defaultOrchestratorAgent is required")
	}
	if !d.DefaultOrchestratorAgent.IsKnown() {
		return fmt.Errorf("defaultOrchestratorAgent: unknown harness %q", d.DefaultOrchestratorAgent)
	}
	return nil
}
