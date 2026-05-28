// Package runfile manages running.json — the PID + port handshake the Electron
// main process uses to discover, health-check, and reap the daemon. The daemon
// writes it on startup and removes it on graceful shutdown. On startup the
// daemon also checks for a stale entry left by a crashed predecessor so it can
// fail fast instead of fighting over the port.
package runfile

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Info is the on-disk handshake payload.
type Info struct {
	// PID is the daemon process id.
	PID int `json:"pid"`
	// Port is the loopback port the daemon bound.
	Port int `json:"port"`
	// StartedAt is when the daemon came up (RFC 3339).
	StartedAt time.Time `json:"startedAt"`
}

// Write atomically writes running.json at path, creating parent directories as
// needed. It writes to a temp file and renames so a reader never observes a
// partial file. os.Rename overwrites an existing destination on both POSIX
// (rename(2) is atomic by default) and Windows (Go uses MoveFileExW with
// MOVEFILE_REPLACE_EXISTING) when source and destination are on the same
// volume, which is always true here because the temp file lives in the same
// directory as the target.
func Write(path string, info Info) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create run-file dir: %w", err)
	}
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal run-file: %w", err)
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(filepath.Dir(path), ".running-*.json")
	if err != nil {
		return fmt.Errorf("create temp run-file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp run-file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp run-file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename run-file into place: %w", err)
	}
	return nil
}

// Read loads running.json. A missing file returns (nil, nil) — that is the
// normal "no daemon recorded" state, not an error.
func Read(path string) (*Info, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read run-file: %w", err)
	}
	var info Info
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, fmt.Errorf("parse run-file: %w", err)
	}
	return &info, nil
}

// Remove deletes running.json. A missing file is not an error — graceful
// shutdown should be idempotent.
func Remove(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove run-file: %w", err)
	}
	return nil
}

// CheckStale inspects an existing run-file before the new daemon binds. It
// returns:
//
//   - (nil, nil)        no run-file, or one left by a dead process (safe to
//     proceed; the caller should overwrite it);
//   - (*Info, nil)      a run-file whose recorded PID is still alive — a live
//     daemon already owns the port, so the caller should fail fast.
//
// A run-file pointing at a dead PID is treated as stale and reported safe; the
// fresh Write will overwrite it.
func CheckStale(path string) (*Info, error) {
	info, err := Read(path)
	if err != nil {
		return nil, err
	}
	if info == nil || info.PID <= 0 {
		return nil, nil
	}
	if processAlive(info.PID) {
		return info, nil
	}
	return nil, nil
}

