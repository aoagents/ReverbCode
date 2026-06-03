//go:build windows

package terminal

import (
	"context"
	"errors"
)

// defaultSpawn is not implemented on Windows: the POSIX PTY path uses
// creack/pty. The rest of the package compiles and tests on Windows with an
// injected spawner.
func defaultSpawn(_ context.Context, _ []string) (ptyProcess, error) {
	return nil, errors.New("terminal: PTY streaming is not supported on Windows yet")
}
