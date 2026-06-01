package inbox_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/messenger/inbox"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// FilenameFor is the shared helper used by both the inbox messenger (writes the
// file) and the panep messenger (points at the file). Both must agree on the
// exact name for the same (timestamp, message) input.
func TestFilenameFor_IsDeterministicForSameInput(t *testing.T) {
	ts := time.Unix(1717171717, 42).UTC()
	body := "ε ao test"
	if got, want := inbox.FilenameFor(ts, body), inbox.FilenameFor(ts, body); got != want {
		t.Fatalf("FilenameFor not deterministic: %q vs %q", got, want)
	}
}

func TestFilenameFor_MatchesTimestampNanoAndHashPrefix(t *testing.T) {
	ts := time.Unix(1717171717, 42).UTC()
	body := "hello agent"

	got := inbox.FilenameFor(ts, body)

	sum := sha256.Sum256([]byte(body))
	wantPrefix := strconv.FormatInt(ts.UnixNano(), 10) + "_" + hex.EncodeToString(sum[:])[:8] + ".md"
	if got != wantPrefix {
		t.Fatalf("FilenameFor(%v, %q) = %q, want %q", ts, body, got, wantPrefix)
	}
}

func TestFilenameFor_DiffersByMessage(t *testing.T) {
	ts := time.Unix(1717171717, 42).UTC()
	a := inbox.FilenameFor(ts, "alpha")
	b := inbox.FilenameFor(ts, "beta")
	if a == b {
		t.Fatalf("different messages produced same filename: %q", a)
	}
}

func TestSatisfiesAgentMessenger(t *testing.T) {
	var _ ports.AgentMessenger = (*inbox.Messenger)(nil)
}

type fakeLookup struct {
	path string
	err  error
}

func (f fakeLookup) WorkspacePath(context.Context, domain.SessionID) (string, error) {
	return f.path, f.err
}

func TestSend_WritesMessageFile(t *testing.T) {
	dir := t.TempDir()
	m := inbox.New(fakeLookup{path: dir})
	if err := m.Send(context.Background(), "s-1", "hello agent"); err != nil {
		t.Fatal(err)
	}
	inboxDir := filepath.Join(dir, ".ao", "inbox")
	entries, err := os.ReadDir(inboxDir)
	if err != nil {
		t.Fatalf("inbox dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("want 1 file, got %d", len(entries))
	}
	name := entries[0].Name()
	if !strings.HasSuffix(name, ".md") {
		t.Errorf("want .md suffix, got %q", name)
	}
	body, err := os.ReadFile(filepath.Join(inboxDir, name))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "hello agent" {
		t.Errorf("body %q want %q", body, "hello agent")
	}
}

func TestSend_CreatesInboxDirIfMissing(t *testing.T) {
	dir := t.TempDir()
	// dir contains no .ao yet.
	m := inbox.New(fakeLookup{path: dir})
	if err := m.Send(context.Background(), "s-1", "x"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".ao", "inbox")); err != nil {
		t.Fatalf("inbox dir not created: %v", err)
	}
}

func TestSend_TwoSendsProduceTwoFiles(t *testing.T) {
	dir := t.TempDir()
	m := inbox.New(fakeLookup{path: dir})
	ctx := context.Background()
	if err := m.Send(ctx, "s-1", "first"); err != nil {
		t.Fatal(err)
	}
	if err := m.Send(ctx, "s-1", "second"); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, ".ao", "inbox"))
	if len(entries) != 2 {
		t.Fatalf("want 2 files, got %d", len(entries))
	}
}

func TestSend_UnknownSessionReturnsError(t *testing.T) {
	m := inbox.New(fakeLookup{err: errors.New("not found")})
	err := m.Send(context.Background(), "s-1", "x")
	if err == nil {
		t.Fatal("expected error when workspace lookup fails")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should wrap lookup error, got %v", err)
	}
}

func TestSend_EmptyWorkspacePathReturnsError(t *testing.T) {
	// A spawned-but-not-yet-mark-spawned row has WorkspacePath == "". The
	// messenger must refuse rather than write into "/.ao/inbox/...".
	m := inbox.New(fakeLookup{path: ""})
	if err := m.Send(context.Background(), "s-1", "x"); err == nil {
		t.Fatal("expected error for empty workspace path")
	}
}

func TestSend_SymlinkedInboxIsRefused(t *testing.T) {
	dir := t.TempDir()
	// Create .ao/inbox as a symlink to a sibling directory.
	target := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".ao"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, ".ao", "inbox")); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	m := inbox.New(fakeLookup{path: dir})
	err := m.Send(context.Background(), "s-1", "x")
	if err == nil {
		t.Fatal("expected refusal when inbox is a symlink")
	}
	if entries, _ := os.ReadDir(target); len(entries) != 0 {
		t.Errorf("symlink target should not have received writes, got %d entries", len(entries))
	}
}

func TestSend_EmptyMessageStillWritesAFile(t *testing.T) {
	dir := t.TempDir()
	m := inbox.New(fakeLookup{path: dir})
	if err := m.Send(context.Background(), "s-1", ""); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, ".ao", "inbox"))
	if len(entries) != 1 {
		t.Fatalf("want 1 file even for empty message, got %d", len(entries))
	}
}

func TestSend_FilenameContainsTimestampAndHashPrefix(t *testing.T) {
	dir := t.TempDir()
	m := inbox.New(fakeLookup{path: dir})
	if err := m.Send(context.Background(), "s-1", "payload"); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, ".ao", "inbox"))
	name := strings.TrimSuffix(entries[0].Name(), ".md")
	// Format: <unix_nano>_<hash>; underscore separator avoids the timestamp's own dashes.
	parts := strings.SplitN(name, "_", 2)
	if len(parts) != 2 {
		t.Fatalf("filename should be <timestamp>_<hash>.md, got %q", entries[0].Name())
	}
	if len(parts[1]) < 4 {
		t.Errorf("hash prefix too short: %q", parts[1])
	}
}
