//go:build !windows

package terminal

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
)

// defaultSpawn starts argv on a real PTY via creack/pty, sized rows×cols from
// birth when a size is known: `zellij attach` reads the tty size once at
// startup, and a post-spawn TIOCSWINSZ depends on SIGWINCH delivery that can
// race the client installing its handler — StartWithSize makes the first read
// correct by construction. env, when non-nil, replaces the inherited
// environment (mirrors exec.Cmd.Env semantics). ctx cancellation closes the PTY
// through the same graceful detach path as an explicit client close. Windows uses
// a stub (see pty_windows.go) until a ConPTY path is added.
func defaultSpawn(ctx context.Context, argv, env []string, rows, cols uint16) (ptyProcess, error) {
	if len(argv) == 0 {
		return nil, errors.New("terminal: empty attach command")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	if env != nil {
		cmd.Env = env
	}
	var f *os.File
	var err error
	if rows > 0 && cols > 0 {
		f, err = pty.StartWithSize(cmd, &pty.Winsize{Rows: rows, Cols: cols})
	} else {
		f, err = pty.Start(cmd)
	}
	if err != nil {
		return nil, err
	}
	proc := &creackPTY{f: f, cmd: cmd}
	go func() {
		<-ctx.Done()
		_ = proc.Close()
	}()
	return proc, nil
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
	err := pty.Setsize(p.f, &pty.Winsize{Rows: rows, Cols: cols})
	// Always follow with an explicit SIGWINCH: the kernel only raises one when
	// the size actually changed, so a re-asserted (identical) grid would never
	// reach a zellij client that missed or lost the original signal — the
	// session would stay laid out for a stale size, with no repaint until the
	// next real change (the frontend re-sends its grid after each resize burst
	// for exactly this self-heal; see useTerminalSession). The client re-reads
	// the tty and re-reports to its server; when already in sync it's a no-op.
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Signal(syscall.SIGWINCH)
	}
	return err
}

// detachGrace is how long Close waits for a SIGTERM'd attach process to exit
// on its own before falling back to SIGKILL. A zellij client that is being
// drained detaches in ~50ms; the grace only runs out for a wedged process.
const detachGrace = 250 * time.Millisecond

// Close stops the attach process and releases the PTY.
//
// SIGTERM first, SIGKILL as fallback: a SIGTERM'd `zellij attach` deregisters
// itself from the zellij server before exiting, while a SIGKILL'd one leaves
// deregistration to the server noticing the dead socket. A dead-but-registered
// client pins the session's size (zellij sizes a session to its smallest
// client), so the next attach renders for the ghost's grid — the "terminal
// doesn't repaint to the new size" desync. The master stays open through the
// grace so the run loop's copyOut keeps draining the client's shutdown output
// (a blocked tty write would stall the graceful exit past the grace).
//
// It is idempotent: both the attachment run loop (after copyOut returns) and
// attachment.close (via closeTerminal, conn cleanup, or Manager.Close) call
// Close on the same PTY, and cmd.Wait must run exactly once. A second
// concurrent Wait on the same process blocks forever, deadlocking daemon
// shutdown when a terminal is still attached.
func (p *creackPTY) Close() error {
	p.closeOnce.Do(func() {
		done := make(chan struct{})
		go func() {
			_ = p.cmd.Wait()
			close(done)
		}()
		if p.cmd.Process != nil {
			_ = p.cmd.Process.Signal(syscall.SIGTERM)
		}
		select {
		case <-done:
		case <-time.After(detachGrace):
			if p.cmd.Process != nil {
				_ = p.cmd.Process.Kill()
			}
			<-done
		}
		p.closeErr = p.f.Close()
	})
	return p.closeErr
}
