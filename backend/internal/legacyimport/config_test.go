package legacyimport

import (
	"path/filepath"
	"testing"
)

// realisticLegacyConfig mirrors the shape `ao project add` writes today: a
// structured `repo:` map (owner/name/platform/originUrl), plus the extra
// top-level keys the rewrite doesn't model. The bare-string `repo` field this
// package first declared made yaml.v3 raise a TypeError on this exact config,
// which dropped the whole registry and hid the dashboard import offer.
const realisticLegacyConfig = `port: 3000
readyThresholdMs: 300000
updateChannel: nightly
defaults:
  runtime: tmux
  agent: claude-code
projects:
  harshitsinghbhandari-github-io:
    projectId: harshitsinghbhandari-github-io
    path: /Users/h/harshitsinghbhandari.github.io
    repo:
      owner: harshitsinghbhandari
      name: harshitsinghbhandari.github.io
      platform: github
      originUrl: https://github.com/harshitsinghbhandari/harshitsinghbhandari.github.io
    defaultBranch: main
    source: ao-project-add
    registeredAt: 1776846948
    displayName: Harshitsinghbhandari.Github.Io
    sessionPrefix: har
    storageKey: 72c8a68fac42
  agent-orchestrator_1a434010b7:
    projectId: agent-orchestrator_1a434010b7
    path: /Users/h/Downloads/agent-orchestrator
    repo:
      owner: harshitsinghbhandari
      name: agent-orchestrator
      platform: github
      originUrl: https://github.com/harshitsinghbhandari/agent-orchestrator
    defaultBranch: develop
    source: ao-project-add
    displayName: Agent Orchestrator
    sessionPrefix: ao
`

func TestLoadLegacyConfig_RepoAsMap(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".agent-orchestrator")
	mustMkdir(t, root)
	mustWrite(t, filepath.Join(root, "config.yaml"), realisticLegacyConfig)

	cfg, err := loadLegacyConfig(root)
	if err != nil {
		t.Fatalf("loadLegacyConfig: %v", err)
	}
	if len(cfg.Projects) != 2 {
		t.Fatalf("parsed %d projects, want 2 (a structured repo map must not drop the registry)", len(cfg.Projects))
	}

	ghio, ok := cfg.Projects["harshitsinghbhandari-github-io"]
	if !ok {
		t.Fatal("missing project harshitsinghbhandari-github-io")
	}
	if ghio.SessionPrefix != "har" || ghio.DefaultBranch != "main" {
		t.Fatalf("ghio = %+v, want sessionPrefix=har defaultBranch=main", ghio)
	}
	if ghio.Repo == nil {
		t.Fatal("repo node should be captured (not consumed, just parsed without error)")
	}

	ao, ok := cfg.Projects["agent-orchestrator_1a434010b7"]
	if !ok {
		t.Fatal("missing project agent-orchestrator_1a434010b7")
	}
	if ao.Path != "/Users/h/Downloads/agent-orchestrator" || ao.DefaultBranch != "develop" {
		t.Fatalf("ao = %+v, want path + defaultBranch=develop", ao)
	}

	// HasLegacyData drives the dashboard offer: it must see these projects.
	if !HasLegacyData(root) {
		t.Fatal("HasLegacyData = false for a real config with a repo map, want true")
	}
}

// TestLoadLegacyConfig_TolerateTypeError covers the defense-in-depth path: a
// field that drifts to an unexpected YAML type (here `path` as a map) raises a
// *yaml.TypeError, but the importer must keep every field/project yaml.v3 still
// decoded rather than discarding the whole registry.
func TestLoadLegacyConfig_TolerateTypeError(t *testing.T) {
	const cfgYAML = `projects:
  good:
    path: /repos/good
    defaultBranch: main
    sessionPrefix: gd
  bad:
    path:
      nested: oops
    sessionPrefix: bd
`
	root := filepath.Join(t.TempDir(), ".agent-orchestrator")
	mustMkdir(t, root)
	mustWrite(t, filepath.Join(root, "config.yaml"), cfgYAML)

	cfg, err := loadLegacyConfig(root)
	if err != nil {
		t.Fatalf("loadLegacyConfig should tolerate a TypeError, got: %v", err)
	}
	if len(cfg.Projects) != 2 {
		t.Fatalf("parsed %d projects, want 2 (partial decode must survive)", len(cfg.Projects))
	}
	if good := cfg.Projects["good"]; good.Path != "/repos/good" || good.SessionPrefix != "gd" {
		t.Fatalf("good project lost fields: %+v", good)
	}
	// The mistyped field is empty, but its sibling keys still decoded.
	if bad := cfg.Projects["bad"]; bad.SessionPrefix != "bd" {
		t.Fatalf("bad project should keep its well-typed siblings: %+v", bad)
	}
	if !HasLegacyData(root) {
		t.Fatal("HasLegacyData = false despite a partial parse, want true")
	}
}

// TestLoadLegacyConfig_SyntaxErrorIsFatal guards the boundary: a genuinely
// malformed document (not a type mismatch) must still hard-fail.
func TestLoadLegacyConfig_SyntaxErrorIsFatal(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".agent-orchestrator")
	mustMkdir(t, root)
	mustWrite(t, filepath.Join(root, "config.yaml"), "projects: : : not yaml\n  - [unbalanced\n")

	if _, err := loadLegacyConfig(root); err == nil {
		t.Fatal("expected a syntax error to be fatal, got nil")
	}
}
