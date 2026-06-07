package project

import "github.com/aoagents/agent-orchestrator/backend/internal/domain"

// Summary is the row shape returned by GET /api/v1/projects.
type Summary struct {
	ID            domain.ProjectID `json:"id"`
	Name          string           `json:"name"`
	SessionPrefix string           `json:"sessionPrefix"`
	ResolveError  string           `json:"resolveError,omitempty"`
}

// Project is the full read-model returned by GET /api/v1/projects/{id}.
type Project struct {
	ID            domain.ProjectID      `json:"id"`
	Name          string                `json:"name"`
	Path          string                `json:"path"`
	Repo          string                `json:"repo"`
	DefaultBranch string                `json:"defaultBranch"`
	Agent         string                `json:"agent,omitempty"`
	Config        *domain.ProjectConfig `json:"config,omitempty"`
}

// Degraded is returned in place of Project when project config failed to load.
type Degraded struct {
	ID           domain.ProjectID `json:"id"`
	Name         string           `json:"name"`
	Path         string           `json:"path"`
	ResolveError string           `json:"resolveError"`
}
