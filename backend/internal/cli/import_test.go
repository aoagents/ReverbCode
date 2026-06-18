package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/legacyimport"
	"github.com/aoagents/agent-orchestrator/backend/internal/runfile"
)

func writeLegacyProject(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), ".agent-orchestrator")
	if err := os.MkdirAll(filepath.Join(root, "projects", "alpha", "sessions"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config.yaml"),
		[]byte("projects:\n  alpha:\n    path: /repos/alpha\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestImportCommand_NoLegacyData(t *testing.T) {
	setConfigEnv(t)
	empty := filepath.Join(t.TempDir(), "nope")
	out, _, err := executeCLI(t, Deps{}, "import", "--from", empty, "--yes")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if !strings.Contains(out, "Nothing to import") {
		t.Fatalf("out = %q, want 'Nothing to import'", out)
	}
}

func TestImportCommand_ImportsProjectJSON(t *testing.T) {
	setConfigEnv(t)
	root := writeLegacyProject(t)

	out, _, err := executeCLI(t, Deps{}, "import", "--from", root, "--yes", "--json")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	var rep legacyimport.Report
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("parse report %q: %v", out, err)
	}
	if rep.ProjectsImported != 1 {
		t.Fatalf("projectsImported = %d, want 1", rep.ProjectsImported)
	}
}

func TestImportCommand_RefusesWhenDaemonRunning(t *testing.T) {
	cfg := setConfigEnv(t)
	root := writeLegacyProject(t)

	// A run-file owned by this (alive) process makes the daemon look live.
	if err := runfile.Write(cfg.runFile, runfile.Info{PID: os.Getpid(), Port: 3001, StartedAt: time.Now()}); err != nil {
		t.Fatalf("write run-file: %v", err)
	}

	_, _, err := executeCLI(t, Deps{}, "import", "--from", root, "--yes")
	if err == nil || !strings.Contains(err.Error(), "daemon is running") {
		t.Fatalf("err = %v, want refusal because daemon is running", err)
	}
}
