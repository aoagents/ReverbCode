//go:build !windows

package runfile

import (
	"errors"
	"os"
	"syscall"
)

// processAlive probes existence with signal 0: kill(pid, 0) returns nil if the
// process exists and we can signal it, EPERM if it exists but is owned by
// another user, and ESRCH (or any other error from FindProcess) if it is gone.
func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	return errors.Is(err, syscall.EPERM)
}
