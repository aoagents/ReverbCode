package sqlite

import (
	"testing"

	"github.com/pressly/goose/v3"
)

// TestMigrationVersionsAreUnique guards against #333: two parallel PRs each
// added a migration file with the same numeric prefix (0014_review_run_retry_failed.sql
// and 0014_telemetry_events.sql). Neither PR conflicted on its own, so CI on
// each passed individually; the collision only surfaced after both merged,
// when goose.Up() derives the version from that prefix and panics on the
// duplicate. This test statically scans the embedded migration filenames and
// parses each version with goose.NumericComponent — the same function goose
// itself uses — so two prefixes that parse to the same int64 (e.g. "014" vs
// "0014") are caught as a collision too, not just identical strings. The
// conflict is reported by `go test` with a clear message instead of a goose
// panic at runtime.
func TestMigrationVersionsAreUnique(t *testing.T) {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}

	seen := map[int64]string{} // parsed version -> filename
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			continue
		}

		version, err := goose.NumericComponent(name)
		if err != nil {
			t.Errorf("migration %q has no version goose can parse: %v", name, err)
			continue
		}

		if other, dup := seen[version]; dup {
			t.Errorf("duplicate migration version %d: %s vs %s", version, other, name)
			continue
		}
		seen[version] = name
	}
}
