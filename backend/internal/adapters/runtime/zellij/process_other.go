//go:build !windows

package zellij

import "errors"

// startBackgroundProcess is a stub: the fire-and-forget path is only used by
// the Windows zellij codepath. Non-Windows builds create sessions
// synchronously via runner.Run.
func startBackgroundProcess(env []string, name string, args ...string) error {
	return errors.New("zellij runtime: background spawn is windows-only")
}
