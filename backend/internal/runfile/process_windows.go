//go:build windows

package runfile

import (
	"syscall"
)

// processAlive opens the process with the minimum-rights query flag. On
// Windows, OpenProcess returns ERROR_INVALID_PARAMETER for a PID that no
// longer maps to a live process, and a usable handle when one is. We close
// the handle immediately; the only thing we needed was the open's outcome.
func processAlive(pid int) bool {
	const PROCESS_QUERY_LIMITED_INFORMATION = 0x1000
	h, err := syscall.OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	_ = syscall.CloseHandle(h)
	return true
}
