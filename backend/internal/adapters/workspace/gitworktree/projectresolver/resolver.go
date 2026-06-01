// Package projectresolver supplies gitworktree.Workspace with a RepoResolver
// backed by the project.Manager. It lives in its own subpackage so the
// gitworktree package can stay free of the project package import (and the
// import cycle that would create if project ever depended on gitworktree).
package projectresolver

import (
	"context"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/workspace/gitworktree"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/project"
)

// Resolver maps a domain.ProjectID to its local repo path by consulting the
// project store via project.Manager.
type Resolver struct {
	projects project.Manager
}

// New builds a Resolver over the given Manager. projects is required.
func New(projects project.Manager) *Resolver {
	return &Resolver{projects: projects}
}

var _ gitworktree.RepoResolver = (*Resolver)(nil)

// RepoPath returns the absolute repo path the project is registered against.
// A degraded project (config failed to load) and an unknown project both yield
// an error rather than the empty path that would silently mis-create worktrees.
//
// The gitworktree.RepoResolver interface is context-free, so we use
// context.Background() to call the underlying Manager.
func (r *Resolver) RepoPath(projectID domain.ProjectID) (string, error) {
	res, err := r.projects.Get(context.Background(), projectID)
	if err != nil {
		return "", fmt.Errorf("projectresolver: lookup %q: %w", projectID, err)
	}
	if res.Project == nil {
		return "", fmt.Errorf("projectresolver: project %q is %s; no repo path available", projectID, res.Status)
	}
	if res.Project.Path == "" {
		return "", fmt.Errorf("projectresolver: project %q has no path", projectID)
	}
	return res.Project.Path, nil
}
