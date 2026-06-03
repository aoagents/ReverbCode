//go:build !windows

package terminal

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"sync"

	"github.com/creack/pty"
)

// defaultSpawn starts argv on a real PTY via creack/pty. ctx cancellation kills
// the process. Windows uses a stub (see pty_windows.go) until a ConPTY path is
// added.
func defaultSpawn(ctx context.Context, argv []string) (ptyProcess, error) {
	if len(argv) == 0 {
		return nil, errors.New("terminal: empty attach command")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	f, err := pty.Start(cmd)
	if err != nil {
		return nil, err
	}
	return &creackPTY{f: f, cmd: cmd}, nil
}

type creackPTY struct {
	f         *os.File
	cmd       *exec.Cmd
	closeOnce sync.Once
	closeErr  error
}

func (p *creackPTY) Read(b []byte) (int, error)  { return p.f.Read(b) }
func (p *creackPTY) Write(b []byte) (int, error) { return p.f.Write(b) }

func (p *creackPTY) Resize(rows, cols uint16) error {
	return pty.Setsize(p.f, &pty.Winsize{Rows: rows, Cols: cols})
}

// Close stops the attach process and releases the PTY. Zellij attach exits cleanly
// when the master closes, but kill the process to be sure it does not linger.
//
// It is idempotent: both the session run loop (after copyOut returns) and
// session.close (via Manager.Close) call Close on the same PTY, and cmd.Wait
// must run exactly once. A second concurrent Wait on the same process blocks
// forever, deadlocking daemon shutdown when a terminal is still attached.
func (p *creackPTY) Close() error {
	p.closeOnce.Do(func() {
		p.closeErr = p.f.Close()
		if p.cmd.Process != nil {
			_ = p.cmd.Process.Kill()
		}
		_ = p.cmd.Wait()
	})
	return p.closeErr
}
