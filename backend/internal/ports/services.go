// Package ports — services.go declares the API-layer service contracts the
// HTTP controllers call. They are distinct from the LCM-facing SessionManager
// (inbound.go) and the persistence/SCM ports (outbound.go): these wrap the
// concerns that only make sense at the HTTP boundary (list filtering,
// enrichment, the "add/remove/repair project" administrative surface).
//
// In the route-shell PR (#20) every implementation is nil — controllers register
// 501 stubs and never call into the services. The interfaces exist so handler-
// impl PRs in the same lane can fill them in without re-touching the wiring.
package ports

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// ProjectService is the API-layer surface for the projects routes. It owns
// administrative concerns (add/remove/repair) that the dashboard exposes but
// the LCM has no business doing.
type ProjectService interface {
	List(ctx context.Context) ([]domain.ProjectSummary, error)
	Get(ctx context.Context, id domain.ProjectID) (GetProjectResult, error)
	Add(ctx context.Context, in AddProjectInput) (domain.Project, error)
	UpdateConfig(ctx context.Context, id domain.ProjectID, patch UpdateProjectConfigInput) (domain.Project, error)
	Remove(ctx context.Context, id domain.ProjectID) (RemoveProjectResult, error)
	Repair(ctx context.Context, id domain.ProjectID) (domain.Project, error)
	Reload(ctx context.Context) (ReloadResult, error)
}

// GetProjectResult is the discriminated union returned by ProjectService.Get.
// Exactly one of Project / Degraded is non-nil. Status mirrors the
// discriminator on the wire so consumers branch on it without nil-checking
// both fields.
type GetProjectResult struct {
	Status   string                  // "ok" | "degraded"
	Project  *domain.Project         // populated when Status == "ok"
	Degraded *domain.DegradedProject // populated when Status == "degraded"
}

// AddProjectInput is the body shape for POST /api/v1/projects. Path is
// required; ProjectID and Name default to basename(path) at the service.
// Pointer fields preserve the "field absent" vs "field present empty"
// distinction so the service can decide what to default and what to reject.
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
// service invalidates its cached config and re-scans the registry; the counts
// help the dashboard show "loaded N projects, M degraded" feedback.
type ReloadResult struct {
	Reloaded      bool `json:"reloaded"`
	ProjectCount  int  `json:"projectCount"`
	DegradedCount int  `json:"degradedCount"`
}
