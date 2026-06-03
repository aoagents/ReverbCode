package cli

import (
	"bytes"
	"strings"
	"testing"
)

// TestSpawnCommand_RequiresProject asserts `ao spawn` rejects a missing
// --project before touching the network, so it fails fast without a daemon.
func TestSpawnCommand_RequiresProject(t *testing.T) {
	var out, errb bytes.Buffer
	root := NewRootCommand(Deps{Out: &out, Err: &errb})
	root.SetArgs([]string{"spawn"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected an error when --project is missing")
	}
	if !strings.Contains(err.Error(), "--project is required") {
		t.Fatalf("error = %v, want it to mention --project is required", err)
	}
}

// TestProjectAddCommand_RequiresPath asserts `ao project add` rejects a missing
// --path before touching the network.
func TestProjectAddCommand_RequiresPath(t *testing.T) {
	var out, errb bytes.Buffer
	root := NewRootCommand(Deps{Out: &out, Err: &errb})
	root.SetArgs([]string{"project", "add"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected an error when --path is missing")
	}
	if !strings.Contains(err.Error(), "--path is required") {
		t.Fatalf("error = %v, want it to mention --path is required", err)
	}
}
