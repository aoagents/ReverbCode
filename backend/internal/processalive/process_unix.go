//go:build !windows

// Package processalive probes whether an operating-system process id still
// maps to a live process.
package processalive

import (
	"errors"
	"syscall"
)

// Alive reports whether pid exists. EPERM counts as alive: the process exists
// even if the current user cannot signal it.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
