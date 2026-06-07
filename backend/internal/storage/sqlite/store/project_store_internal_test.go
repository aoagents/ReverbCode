package store

import (
	"database/sql"
	"testing"
)

func TestUnmarshalProjectConfigDegradesGracefully(t *testing.T) {
	// SQL NULL / empty → zero config.
	if got := unmarshalProjectConfig(sql.NullString{}); !got.IsZero() {
		t.Fatalf("NULL config = %#v, want zero", got)
	}

	// Valid JSON decodes.
	if got := unmarshalProjectConfig(sql.NullString{String: `{"defaultBranch":"develop"}`, Valid: true}); got.DefaultBranch != "develop" {
		t.Fatalf("valid config DefaultBranch = %q, want develop", got.DefaultBranch)
	}

	// Corrupt JSON must NOT error — it degrades to a zero config so the project
	// row (and ListProjects) stay accessible.
	if got := unmarshalProjectConfig(sql.NullString{String: `{not json`, Valid: true}); !got.IsZero() {
		t.Fatalf("corrupt config = %#v, want zero (degraded)", got)
	}
}
