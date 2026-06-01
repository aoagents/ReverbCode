package project

import "github.com/aoagents/agent-orchestrator/backend/internal/domain"

// Request/result shapes for Manager. The entities they reference (Project,
// Summary, Degraded) live in types.go.

// GetResult is the internal result of Manager.Get — not the wire shape, so no
// JSON tags. Exactly one of Project/Degraded is non-nil; Status is the
// discriminator. The controller maps this onto the ProjectGetResponse envelope
// ({status, project}) from openapi.yaml, keeping the oneOf out of this package.
type GetResult struct {
	Status   string    // "ok" | "degraded"
	Project  *Project  // populated when Status == "ok"
	Degraded *Degraded // populated when Status == "degraded"
}

// AddInput is the body for POST /api/v1/projects. Path is required; ProjectID
// and Name default to basename(path). Pointers distinguish absent from empty.
type AddInput struct {
	Path      string  `json:"path" description:"Repository path; supports ~ home-expansion. Must be a git repo."`
	ProjectID *string `json:"projectId,omitempty" description:"Optional override; defaults to basename(path)."`
	Name      *string `json:"name,omitempty" description:"Optional display name; defaults to projectId."`
}

// UpdateConfigInput is the body for PATCH /api/v1/projects/{id}. Only behaviour
// fields are mutable; identity fields are rejected with 400 IDENTITY_FROZEN. A
// nil field means it was absent from the patch.
type UpdateConfigInput struct {
	Agent     AgentConfig                 `json:"agent,omitempty"`
	Runtime   RuntimeConfig               `json:"runtime,omitempty"`
	Tracker   *TrackerConfig              `json:"tracker,omitempty"`
	SCM       *SCMConfig                  `json:"scm,omitempty"`
	Reactions *map[string]*ReactionConfig `json:"reactions,omitempty"`
}

// RemoveResult is the body for DELETE /api/v1/projects/{id}. RemovedStorageDir
// is false when the project was registry-only (no on-disk directory existed).
type RemoveResult struct {
	ProjectID         domain.ProjectID `json:"projectId"`
	RemovedStorageDir bool             `json:"removedStorageDir"`
}
