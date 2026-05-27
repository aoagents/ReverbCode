package runfile

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteReadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "running.json")
	want := Info{PID: 4242, Port: 3001, StartedAt: time.Now().UTC().Truncate(time.Second)}

	if err := Write(path, want); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got == nil {
		t.Fatal("Read returned nil for an existing file")
	}
	if got.PID != want.PID || got.Port != want.Port || !got.StartedAt.Equal(want.StartedAt) {
		t.Errorf("round trip mismatch: got %+v, want %+v", *got, want)
	}
}

func TestReadMissingIsNotError(t *testing.T) {
	got, err := Read(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatalf("Read missing: %v", err)
	}
	if got != nil {
		t.Errorf("Read missing = %+v, want nil", got)
	}
}

func TestRemoveIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "running.json")
	if err := Remove(path); err != nil {
		t.Errorf("Remove on missing file: %v", err)
	}
	if err := Write(path, Info{PID: 1, Port: 2}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := Remove(path); err != nil {
		t.Errorf("Remove existing: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file still present after Remove")
	}
}

func TestCheckStaleDeadPID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "running.json")
	// PID 0x7FFFFFFF is effectively guaranteed not to exist.
	if err := Write(path, Info{PID: 0x7FFFFFFF, Port: 3001}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	live, err := CheckStale(path)
	if err != nil {
		t.Fatalf("CheckStale: %v", err)
	}
	if live != nil {
		t.Errorf("CheckStale on dead PID = %+v, want nil (stale, safe to overwrite)", live)
	}
}

func TestCheckStaleLivePID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "running.json")
	// This test process is unquestionably alive.
	if err := Write(path, Info{PID: os.Getpid(), Port: 3001}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	live, err := CheckStale(path)
	if err != nil {
		t.Fatalf("CheckStale: %v", err)
	}
	if live == nil {
		t.Fatal("CheckStale on live PID = nil, want the live Info")
	}
	if live.PID != os.Getpid() {
		t.Errorf("live.PID = %d, want %d", live.PID, os.Getpid())
	}
}

func TestCheckStaleNoFile(t *testing.T) {
	live, err := CheckStale(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatalf("CheckStale: %v", err)
	}
	if live != nil {
		t.Errorf("CheckStale with no file = %+v, want nil", live)
	}
}
