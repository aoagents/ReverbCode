package sqlite

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// TestMigrateAllowsEveryShippedHarness guards against the collapsed-migration
// silent-no-op concern: a hand-written replace() that fails to widen the
// sessions.harness CHECK (because the target substring drifted) leaves the
// schema accepting only the original harnesses while migrate() still reports
// success. This test opens a fresh DB, runs the migrations, and asserts the
// live sessions schema admits every harness the domain ships, building the
// expected set from the domain constants so it can't silently drift.
func TestMigrateAllowsEveryShippedHarness(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "ao.db")+pragmas)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var schema string
	if err := db.QueryRow(
		"SELECT sql FROM sqlite_master WHERE type='table' AND name='sessions'",
	).Scan(&schema); err != nil {
		t.Fatalf("read sessions schema: %v", err)
	}

	harnesses := []domain.AgentHarness{
		domain.HarnessClaudeCode,
		domain.HarnessCodex,
		domain.HarnessAider,
		domain.HarnessOpenCode,
		domain.HarnessGrok,
		domain.HarnessDroid,
		domain.HarnessAmp,
		domain.HarnessAgy,
		domain.HarnessCrush,
		domain.HarnessCursor,
		domain.HarnessQwen,
		domain.HarnessCopilot,
		domain.HarnessGoose,
		domain.HarnessAuggie,
		domain.HarnessContinue,
		domain.HarnessDevin,
		domain.HarnessCline,
		domain.HarnessKimi,
		domain.HarnessKiro,
		domain.HarnessKilocode,
		domain.HarnessVibe,
		domain.HarnessPi,
		domain.HarnessAutohand,
	}

	for _, h := range harnesses {
		if !strings.Contains(schema, "'"+string(h)+"'") {
			t.Errorf("sessions.harness CHECK is missing harness %q — the migration that widens it silently no-opped; schema:\n%s", h, schema)
		}
	}
}

func TestParseTimestamp(t *testing.T) {
	cases := []struct {
		name   string
		raw    string
		wantOK bool
		want   string
	}{
		{
			name:   "duplicate numeric offset from unnamed FixedZone",
			raw:    "2026-10-17 11:19:16.938849 -0400 -0400",
			wantOK: true,
			want:   "2026-10-17T15:19:16.938849Z",
		},
		{
			name:   "duplicate numeric offset with monotonic suffix",
			raw:    "2026-10-17 11:19:16.938849 -0400 -0400 m=+12.345",
			wantOK: true,
			want:   "2026-10-17T15:19:16.938849Z",
		},
		{
			name:   "rfc3339 nano",
			raw:    "2026-10-17T11:19:16.5-04:00",
			wantOK: true,
			want:   "2026-10-17T15:19:16.5Z",
		},
		{
			name:   "unix seconds string",
			raw:    "1700000000",
			wantOK: true,
			want:   "2023-11-14T22:13:20Z",
		},
		{
			name:   "corrupt string returns false",
			raw:    "not-a-timestamp",
			wantOK: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ParseTimestamp(tc.raw)
			if ok != tc.wantOK {
				t.Fatalf("ParseTimestamp(%q) ok = %v, want %v", tc.raw, ok, tc.wantOK)
			}
			if !tc.wantOK {
				return
			}
			if got.Location().String() != "UTC" {
				t.Fatalf("ParseTimestamp(%q) location = %q, want UTC", tc.raw, got.Location())
			}
			if gotStr := got.Format("2006-01-02T15:04:05.999999999Z07:00"); gotStr != tc.want {
				t.Fatalf("ParseTimestamp(%q) = %q, want %q", tc.raw, gotStr, tc.want)
			}
		})
	}
}

func TestMigrateRepairsDuplicateOffsetTimestamps(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "ao.db")
	db, err := sql.Open(driverName, "file:"+dbPath+pragmas)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := migrate(db); err != nil {
		t.Fatalf("initial migrate: %v", err)
	}

	if _, err := db.Exec(`INSERT INTO projects (id, path, registered_at) VALUES ('p1', '/tmp/p1', '2026-10-17 11:19:16.938849 -0400 -0400')`); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO sessions (id, project_id, num, kind, harness, activity_state, activity_last_at, created_at, updated_at) VALUES ('p1-1', 'p1', 1, 'worker', 'claude-code', 'idle', '2026-10-17 11:19:16.938849 -0400 -0400', '2026-10-17 11:19:16.938849 -0400 -0400', '2026-10-17 11:19:16.938849 -0400 -0400')`); err != nil {
		t.Fatalf("insert session: %v", err)
	}
	// Reset migration 20 so re-running migrate applies 0020_normalize_timestamps.sql against the bad rows.
	if _, err := db.Exec(`DELETE FROM goose_db_version WHERE version_id = 20`); err != nil {
		t.Fatalf("delete goose_db_version 20: %v", err)
	}
	if err := migrate(db); err != nil {
		t.Fatalf("re-migrate 20: %v", err)
	}

	var rawCreated string
	if err := db.QueryRow(`SELECT CAST(created_at AS TEXT) FROM sessions WHERE id = 'p1-1'`).Scan(&rawCreated); err != nil {
		t.Fatalf("select raw created_at: %v", err)
	}
	if want := "2026-10-17 11:19:16.938849 -0400 GMT"; rawCreated != want {
		t.Fatalf("raw created_at after migration 0020 = %q, want %q", rawCreated, want)
	}
}
