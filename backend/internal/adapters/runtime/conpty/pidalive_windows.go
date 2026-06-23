//go:build windows

package conpty

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// pidAlive probes PID liveness on Windows by opening the process handle with
// SYNCHRONIZE (minimal permission). Failure means the process is gone.
func pidAlive(pid int) bool {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return false
	}
	_ = windows.CloseHandle(h)
	return true
}

// defaultOSProcessFinder wraps os.FindProcess for Windows.
func defaultOSProcessFinder(pid int) (processKiller, error) {
	p, err := os.FindProcess(pid)
	if err != nil {
		return nil, fmt.Errorf("os.FindProcess(%d): %w", pid, err)
	}
	return p, nil
}
