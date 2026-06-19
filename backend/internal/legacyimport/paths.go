// Package legacyimport reads the legacy Agent Orchestrator flat-file store
// (~/.agent-orchestrator) read-only and ports it into the rewrite's native
// SQLite store. It maps the legacy project registry, per-project settings, and
// each project's single live orchestrator session, relocating the orchestrator's
// Claude transcript so a claude-code orchestrator resumes with context.
//
// This is the Go port of the legacy-side TypeScript reader (AgentWrapper PR
// #2144 / issue #2129); the field mapping is ReverbCode issue #247. The legacy
// files are NEVER modified: a declined or failed import loses nothing, and a
// re-run skips rows that already exist.
package legacyimport

import (
	"os"
	"path/filepath"
	"strings"
)

// userHomeDir is indirected so tests can pin the home directory without mutating
// process environment.
var userHomeDir = os.UserHomeDir

// DefaultLegacyRootDir returns the canonical legacy state root,
// ~/.agent-orchestrator, or "" when the home directory cannot be resolved.
func DefaultLegacyRootDir() string {
	home, err := userHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".agent-orchestrator")
}

// defaultClaudeProjectsDir returns ~/.claude/projects, the directory Claude Code
// buckets per-cwd transcripts under. "" when home cannot be resolved.
func defaultClaudeProjectsDir() string {
	home, err := userHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "projects")
}

// globalConfigPath is the legacy global config file, root/config.yaml.
func globalConfigPath(root string) string {
	return filepath.Join(root, "config.yaml")
}

// preferencesPath / registeredPath are the optional portfolio overlays that
// carry UI display names and per-project registration timestamps.
func preferencesPath(root string) string {
	return filepath.Join(root, "portfolio", "preferences.json")
}

func registeredPath(root string) string {
	return filepath.Join(root, "portfolio", "registered.json")
}

// projectSessionsDir locates a project's sessions directory, accepting both the
// current layout (root/projects/{id}/sessions) and the older hashed layout
// (root/{hash}-{id}/sessions). It returns "" when neither exists.
func projectSessionsDir(root, projectID string) string {
	primary := filepath.Join(root, "projects", projectID, "sessions")
	if isDir(primary) {
		return primary
	}
	// Older layout: a top-level "{hash}-{id}" directory. Match by the "-{id}"
	// suffix; the id itself may contain "-", but the hashed form always prefixes
	// it, so a suffix match is the faithful locator the legacy reader used.
	entries, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	suffix := "-" + projectID
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if name == "projects" || name == "portfolio" {
			continue
		}
		if strings.HasSuffix(name, suffix) {
			cand := filepath.Join(root, name, "sessions")
			if isDir(cand) {
				return cand
			}
		}
	}
	return ""
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
