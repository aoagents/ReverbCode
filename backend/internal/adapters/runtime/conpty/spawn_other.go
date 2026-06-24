//go:build !windows

// spawn_other.go - stub for non-Windows platforms. The real detached-process
// spawn lives in spawn_windows.go and uses Windows process-creation flags.
package conpty

import (
	"context"
	"errors"
)

// defaultSpawnHost is a stub on non-Windows platforms. Tests inject their own
// spawner; this only needs to keep the package buildable on Darwin/Linux.
func defaultSpawnHost(_ context.Context, _, _ string, _ []string, _ map[string]string) (string, int, error) {
	return "", 0, errors.New("conpty spawn: unsupported on this OS")
}
