package project

import "github.com/aoagents/agent-orchestrator/backend/internal/domain"

// GetResult is the discriminated result returned by Service.Get.
type GetResult struct {
	Status   string
	Project  *Project
	Degraded *Degraded
}

// AddInput is the body shape for POST /api/v1/projects.
type AddInput struct {
	Path        string              `json:"path"`
	ProjectID   *string             `json:"projectId,omitempty"`
	Name        *string             `json:"name,omitempty"`
	AgentConfig *domain.AgentConfig `json:"agentConfig,omitempty"`
}

// SetAgentConfigInput is the body shape for PUT
// /api/v1/projects/{id}/agent-config. Config replaces the project's stored
// agent config wholesale; a zero-value config clears it.
type SetAgentConfigInput struct {
	Config domain.AgentConfig `json:"config"`
}

// RemoveResult reports what DELETE /api/v1/projects/{id} actually did.
type RemoveResult struct {
	ProjectID         domain.ProjectID `json:"projectId"`
	RemovedStorageDir bool             `json:"removedStorageDir"`
}
