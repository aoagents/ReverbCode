// Package importer is the controller-facing service for the legacy-AO import.
// It wraps the internal/legacyimport engine (merged in #314) with the two
// operations the dashboard needs: a detection probe ("is a legacy install
// available to import?") and a trigger that runs the import through the live
// daemon's store. The engine is reused verbatim; this package adds no import
// logic of its own, only the daemon-side detection and the store wiring.
package importer

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/legacyimport"
)

// Store is the storage slice the import service needs: the legacy importer's
// write surface plus a project listing for the "already imported" check.
// *sqlite.Store satisfies it, so the daemon passes its single shared store and
// the import runs through the same write path as every other mutation, so the
// daemon stays the sole writer.
type Store interface {
	legacyimport.Store
	ListProjects(ctx context.Context) ([]domain.ProjectRecord, error)
}

// Status reports whether a legacy AO install is available to import. Available
// is true only when legacy data is present AND the rewrite database holds no
// projects yet, matching the first-boot opt-in condition: a populated rewrite
// is assumed to have already been imported (or started fresh on purpose), so
// the offer is not surfaced again.
type Status struct {
	Available  bool   `json:"available"`
	LegacyRoot string `json:"legacyRoot"`
}

// Service is the controller-facing import contract.
type Service interface {
	Status(ctx context.Context) (Status, error)
	Run(ctx context.Context) (legacyimport.Report, error)
}

// Deps bundles the import service's dependencies.
type Deps struct {
	// Store is the rewrite's durable store (the daemon's shared *sqlite.Store).
	Store Store
	// Root overrides the legacy AO root to read. Empty → the default
	// (~/.agent-orchestrator).
	Root string
}

// Manager implements Service over the daemon's store and config.
type Manager struct {
	store Store
	root  string
}

var _ Service = (*Manager)(nil)

// New constructs the import service. An empty Root falls back to the default
// legacy root so callers that don't override it get the standard location.
func New(deps Deps) *Manager {
	root := deps.Root
	if root == "" {
		root = legacyimport.DefaultLegacyRootDir()
	}
	return &Manager{store: deps.Store, root: root}
}

// Status reports import availability without touching legacy or rewrite data
// beyond a project count. It never errors on a missing legacy store; that is
// simply "not available".
func (m *Manager) Status(ctx context.Context) (Status, error) {
	st := Status{LegacyRoot: m.root}
	if !legacyimport.HasLegacyData(m.root) {
		return st, nil
	}
	projects, err := m.store.ListProjects(ctx)
	if err != nil {
		return Status{}, err
	}
	st.Available = len(projects) == 0
	return st, nil
}

// Run executes the import through the daemon's store. It is idempotent: the
// engine skips rows that already exist, so a re-run (or a run against a
// partially-populated database) is safe and never overwrites. Legacy files are
// never modified.
func (m *Manager) Run(ctx context.Context) (legacyimport.Report, error) {
	return legacyimport.Run(ctx, m.store, legacyimport.Options{Root: m.root})
}
