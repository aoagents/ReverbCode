package adapters

import (
	"fmt"
	"sort"
)

// Capability identifies a feature an adapter provides, such as running an
// agent or backing an issue tracker.
type Capability string

// Capabilities an adapter can advertise in its Manifest.
const (
	CapabilityAgent        Capability = "agent"
	CapabilityIssueTracker Capability = "issue-tracker"
)

// Manifest is an adapter's self-description: its id, human-facing name and
// description, version, and the capabilities it provides.
type Manifest struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	Description  string       `json:"description"`
	Version      string       `json:"version"`
	Capabilities []Capability `json:"capabilities"`
}

// Adapter is the minimal contract every registered adapter satisfies.
type Adapter interface {
	Manifest() Manifest
}

// Registry holds registered adapters keyed by their manifest id.
//
// Registry is not safe for concurrent registration: every Register call is
// expected at daemon boot, before any goroutine calls Get. Concurrent
// Register and Get would race on the underlying map.
type Registry struct {
	adapters map[string]Adapter
}

// NewRegistry returns an empty Registry ready to Register adapters.
func NewRegistry() *Registry {
	return &Registry{
		adapters: make(map[string]Adapter),
	}
}

// Register adds adapter under its manifest id. It returns an error when the id
// is empty or already registered.
func (r *Registry) Register(adapter Adapter) error {
	manifest := adapter.Manifest()
	if manifest.ID == "" {
		return fmt.Errorf("adapter id is required")
	}
	if _, exists := r.adapters[manifest.ID]; exists {
		return fmt.Errorf("adapter %q is already registered", manifest.ID)
	}

	r.adapters[manifest.ID] = adapter
	return nil
}

// Get returns the registered adapter with the given id, or nil and false
// when no such adapter exists.
func (r *Registry) Get(id string) (Adapter, bool) {
	p, ok := r.adapters[id]
	return p, ok
}

// Manifests returns every registered adapter's manifest, sorted by id.
func (r *Registry) Manifests() []Manifest {
	manifests := make([]Manifest, 0, len(r.adapters))
	for _, adapter := range r.adapters {
		manifests = append(manifests, adapter.Manifest())
	}

	sort.Slice(manifests, func(i, j int) bool {
		return manifests[i].ID < manifests[j].ID
	})

	return manifests
}
