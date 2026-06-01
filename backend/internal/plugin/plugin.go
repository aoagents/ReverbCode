package plugin

import (
	"fmt"
	"sort"
)

type Capability string

const (
	CapabilityAgent        Capability = "agent"
	CapabilityIssueTracker Capability = "issue-tracker"
)

type Manifest struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	Description  string       `json:"description"`
	Version      string       `json:"version"`
	Capabilities []Capability `json:"capabilities"`
}

type Plugin interface {
	Manifest() Manifest
}

type Registry struct {
	plugins map[string]Plugin
}

func NewRegistry() *Registry {
	return &Registry{
		plugins: make(map[string]Plugin),
	}
}

func (r *Registry) Register(plugin Plugin) error {
	manifest := plugin.Manifest()
	if manifest.ID == "" {
		return fmt.Errorf("plugin id is required")
	}
	if _, exists := r.plugins[manifest.ID]; exists {
		return fmt.Errorf("plugin %q is already registered", manifest.ID)
	}

	r.plugins[manifest.ID] = plugin
	return nil
}

// Get returns the registered plugin with the given id, or nil and false
// when no such plugin exists.
func (r *Registry) Get(id string) (Plugin, bool) {
	p, ok := r.plugins[id]
	return p, ok
}

func (r *Registry) Manifests() []Manifest {
	manifests := make([]Manifest, 0, len(r.plugins))
	for _, plugin := range r.plugins {
		manifests = append(manifests, plugin.Manifest())
	}

	sort.Slice(manifests, func(i, j int) bool {
		return manifests[i].ID < manifests[j].ID
	})

	return manifests
}
