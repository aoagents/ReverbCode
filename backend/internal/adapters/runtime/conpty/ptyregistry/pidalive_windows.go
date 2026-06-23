//go:build windows

package ptyregistry

import (
	"golang.org/x/sys/windows"
)

// defaultPidAlive probes PID liveness via OpenProcess. SUCCESS means alive
// (CloseHandle and return true). ERROR_ACCESS_DENIED mirrors EPERM: the
// process exists but cannot be queried, so treat as alive.
func defaultPidAlive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err == nil {
		_ = windows.CloseHandle(h)
		return true
	}
	return err == windows.ERROR_ACCESS_DENIED
}
