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
}
