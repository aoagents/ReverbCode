//go:build windows

package cli

import (
	"syscall"

	"golang.org/x/sys/windows"
)

// detachSysProcAttr starts the daemon in a new process group so it does not
// receive the console's CTRL_C/CTRL_BREAK while `ao start` waits for readiness.
func detachSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
}
