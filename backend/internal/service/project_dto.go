package service

import "github.com/aoagents/agent-orchestrator/backend/internal/domain"

// GetProjectResult is the discriminated result returned by ProjectService.Get.
type GetProjectResult struct {
	Status   string
	Project  *Project
	Degraded *DegradedProject
}

// AddProjectInput is the body shape for POST /api/v1/projects.
type AddProjectInput struct {
	Path      string  `json:"path"`
	ProjectID *string `json:"projectId,omitempty"`
	Name      *string `json:"name,omitempty"`
}

// RemoveProjectResult reports what DELETE /api/v1/projects/{id} actually did.
type RemoveProjectResult struct {
	ProjectID         domain.ProjectID `json:"projectId"`
	RemovedStorageDir bool             `json:"removedStorageDir"`
}
