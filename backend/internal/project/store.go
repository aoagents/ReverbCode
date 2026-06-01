package project

import (
	"context"
	"time"
)

// Row mirrors the project table shape from the sqlite storage layer. The manager
// consumes rows through the Store port below; the sqlite store returns this same
// shape, so the API layer never depends on a richer model than the DB provides.
type Row struct {
	ID            string
	Path          string
	RepoOriginURL string
	DisplayName   string
	RegisteredAt  time.Time
	ArchivedAt    time.Time
}

// Store is the project persistence port the manager talks to. It exists to
// invert the dependency: the storage layer imports this package (for Row), so
// the manager reaches the backend through this interface rather than importing
// the concrete *sqlite.Store — which would create an import cycle
// (project → sqlite → store → project). The real *sqlite.Store satisfies it;
// tests pass a real temp-dir sqlite store. There is no in-memory implementation.
type Store interface {
	List(ctx context.Context) ([]Row, error)
	Get(ctx context.Context, id string) (Row, bool, error)
	FindByPath(ctx context.Context, path string) (Row, bool, error)
	Upsert(ctx context.Context, row Row) error
	Archive(ctx context.Context, id string, at time.Time) (bool, error)
}
