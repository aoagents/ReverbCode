// Package project owns the projects service contract: the Manager interface and
// the DTOs that cross it (dto.go), plus the project entities (types.go).
//
// Manager is an application-service contract reused across protocols (HTTP
// today, CLI next), so it lives in the feature package rather than beside one
// consumer — mirroring ports.SessionManager. This is the pilot for the
// feature-package layout: a resource's interface, entities, and DTOs live with
// the resource. Consumers depend on Manager and nothing beneath it; what the
// impl reaches into (config registry, LCM, workspace adapter) is its own
// concern and lands in the handler-impl PR. This PR defines only the contract.
//
// Reload and Repair are absent by design: the route analysis dropped reload
// and deferred repair.
package project

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Manager is the inbound contract for project operations, called by the HTTP
// controller today and the CLI later.
type Manager interface {
	// List returns every registered project, including degraded entries
	// (those whose config failed to load but whose registry entry survives).
	List(ctx context.Context) ([]Summary, error)

	// Get returns one project, discriminating ok vs degraded via GetResult.
	Get(ctx context.Context, id domain.ProjectID) (GetResult, error)

	// Add registers a new project from a git repository path.
	Add(ctx context.Context, in AddInput) (Project, error)

	// UpdateConfig patches behaviour-only fields; identity fields are frozen.
	UpdateConfig(ctx context.Context, id domain.ProjectID, patch UpdateConfigInput) (Project, error)

	// Remove unregisters a project, stopping its sessions and reclaiming
	// managed workspaces.
	Remove(ctx context.Context, id domain.ProjectID) (RemoveResult, error)
}
