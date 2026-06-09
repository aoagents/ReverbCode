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
	// Config holds the typed per-project configuration AO resolves at spawn. An
	// IsZero value means unset.
	Config ProjectConfig
}
