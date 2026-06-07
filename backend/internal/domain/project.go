package domain

import "time"

// ProjectRecord is the durable project registry row used by storage and services.
type ProjectRecord struct {
	ID            string
	Path          string
	RepoOriginURL string
	DisplayName   string
	RegisteredAt  time.Time
	ArchivedAt    time.Time
	// AgentConfig holds the typed per-project agent settings (model,
	// permissions) AO resolves into a launch command at spawn. An IsZero value
	// means unset.
	AgentConfig AgentConfig
}
