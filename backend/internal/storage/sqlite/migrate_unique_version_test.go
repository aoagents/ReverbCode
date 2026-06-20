package sqlite

import (
	"testing"
)

// TestMigrationVersionsAreUnique guards against #333: two parallel PRs each
// added a migration file with the same numeric prefix (0014_review_run_retry_failed.sql
// and 0014_telemetry_events.sql). Neither PR conflicted on its own, so CI on
// each passed individually; the collision only surfaced after both merged,
// when goose.Up() derives the version from that prefix and panics on the
// duplicate. This test statically scans the embedded migration filenames for
// a repeated version prefix, so the conflict is caught by `go test` with a
// clear message instead of a goose panic at runtime.
func TestMigrationVersionsAreUnique(t *testing.T) {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}

	seen := map[string]string{} // version prefix -> filename
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			continue
		}

		prefix := versionPrefix(name)
		if prefix == "" {
			t.Errorf("migration %q has no leading numeric version prefix that goose can parse", name)
			continue
		}

		if other, dup := seen[prefix]; dup {
			t.Errorf("duplicate migration version %s: %s vs %s", prefix, other, name)
			continue
		}
		seen[prefix] = name
	}
}

// versionPrefix returns the leading run of digits before the first
// underscore in a migration filename — the same substring goose parses as
// the migration's version number. It returns "" if the filename has no
// underscore or no leading digits.
func versionPrefix(filename string) string {
	idx := -1
	for i, c := range filename {
		if c == '_' {
			idx = i
			break
		}
	}
	if idx <= 0 {
		return ""
	}
	digits := filename[:idx]
	for _, c := range digits {
		if c < '0' || c > '9' {
			return ""
		}
	}
	return digits
}
