package hookutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureWorkspaceGitignoreWritesSelfIgnoringFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".codex")
	if err := EnsureWorkspaceGitignore(dir, "hooks.json", "config.toml"); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, GitignoreSentinel) {
		t.Fatalf("content missing sentinel: %q", content)
	}
	// Entries are anchored so only AO's files in THIS directory are ignored —
	// an agent's own files (even in the same dir) must keep counting as dirt.
	for _, want := range []string{"/.gitignore\n", "/hooks.json\n", "/config.toml\n"} {
		if !strings.Contains(content, want) {
			t.Errorf("content missing entry %q: %q", want, content)
		}
	}
}

func TestEnsureWorkspaceGitignoreIsIdempotent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".codex")
	if err := EnsureWorkspaceGitignore(dir, "hooks.json"); err != nil {
		t.Fatalf("first ensure: %v", err)
	}
	first, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if err := EnsureWorkspaceGitignore(dir, "hooks.json"); err != nil {
		t.Fatalf("second ensure: %v", err)
	}
	second, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("rewrite changed content:\nfirst:  %q\nsecond: %q", first, second)
	}
}

func TestEnsureWorkspaceGitignoreLeavesForeignFileUntouched(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".codex")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	foreign := "# user rules\n*.log\n"
	path := filepath.Join(dir, ".gitignore")
	if err := os.WriteFile(path, []byte(foreign), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := EnsureWorkspaceGitignore(dir, "hooks.json"); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != foreign {
		t.Fatalf("foreign .gitignore was modified: %q", data)
	}
}
