package ports

import "github.com/aoagents/agent-orchestrator/backend/internal/domain"

// DTOs for ProjectManager (declared in inbound.go alongside its peers
// SessionManager and LifecycleManager). The interface is the boundary
// controllers care about; these types carry the request/response shapes
// across that boundary. Whether the manager impl reaches into the registry,
// the LCM, or an adapter is a private concern — controllers see only the
// types here and the methods on ProjectManager.

// GetProjectResult is the discriminated union returned by ProjectManager.Get.
// Exactly one of Project / Degraded is non-nil. Status mirrors the
// discriminator on the wire so consumers branch on it without nil-checking
// both fields.
type GetProjectResult struct {
	Status   string                  // "ok" | "degraded"
	Project  *domain.Project         // populated when Status == "ok"
	Degraded *domain.DegradedProject // populated when Status == "degraded"
}

// AddProjectInput is the body shape for POST /api/v1/projects. Path is
// required; ProjectID and Name default to basename(path) at the manager.
// Pointer fields preserve the "field absent" vs "field present empty"
// distinction so the manager can decide what to default and what to reject.
type AddProjectInput struct {
	Path      string  `json:"path"`
	ProjectID *string `json:"projectId,omitempty"`
	Name      *string `json:"name,omitempty"`
}

// UpdateProjectConfigInput is the body shape for PATCH /api/v1/projects/{id}.
// Only behaviour fields are mutable; identity fields (projectId, path, repo,
// defaultBranch) are rejected by the handler with a 400 IDENTITY_FROZEN.
type UpdateProjectConfigInput struct {
	Agent     *string                            `json:"agent,omitempty"`
	Runtime   *string                            `json:"runtime,omitempty"`
	Tracker   *domain.TrackerConfig              `json:"tracker,omitempty"`
	SCM       *domain.SCMConfig                  `json:"scm,omitempty"`
	Reactions *map[string]*domain.ReactionConfig `json:"reactions,omitempty"`
}

// RemoveProjectResult reports what DELETE /api/v1/projects/{id} actually did.
// RemovedStorageDir is false when the project was registry-only (no on-disk
// session/workspace directory existed).
type RemoveProjectResult struct {
	ProjectID         domain.ProjectID `json:"projectId"`
	RemovedStorageDir bool             `json:"removedStorageDir"`
}

// ReloadResult is the response body of POST /api/v1/projects/reload — the
// manager invalidates its cached config and re-scans the registry; the
// counts help the dashboard show "loaded N projects, M degraded" feedback.
type ReloadResult struct {
	Reloaded      bool `json:"reloaded"`
	ProjectCount  int  `json:"projectCount"`
	DegradedCount int  `json:"degradedCount"`
}
