//go:build windows

// spawn_windows.go - real detached pty-host spawner for Windows using
// CREATE_NEW_PROCESS_GROUP + DETACHED_PROCESS so the host survives daemon exit.
package conpty

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

// readyRE matches the "READY:<pid> <port>" line printed by RunHost.
var readyRE = regexp.MustCompile(`READY:(\d+) (\d+)`)

const spawnReadyTimeout = 10 * time.Second

// defaultSpawnHost resolves the current executable, builds the pty-host argv,
// and spawns it detached on Windows. It reads stdout for "READY:<pid> <port>"
// with a 10s timeout, then unrefs (detaches) the child. Returns the loopback
// address and the pty-host OS PID.
func defaultSpawnHost(ctx context.Context, sessionID, cwd string, argv []string, env map[string]string) (string, int, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", 0, fmt.Errorf("conpty spawn: resolve executable: %w", err)
	}

	// Build: <exe> pty-host <sessionID> <cwd> <shellCmd> <shellArgs...>
	args := append([]string{"pty-host", sessionID, cwd}, argv...)

	// Merge env: inherit parent, then overlay caller-provided vars.
	merged := os.Environ()
	for k, v := range env {
		merged = append(merged, k+"="+v)
	}

	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Dir = cwd
	cmd.Env = merged

	// Windows process-creation flags: detached + hidden console.
	// ponytail: DETACHED_PROCESS puts the child in its own console; without it
	// the child is killed when the parent's console closes. CREATE_NEW_PROCESS_GROUP
	// insulates it from Ctrl+C sent to the parent. windowsHide suppresses the flash.
	cmd.SysProcAttr = &windows.SysProcAttr{
		CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP,
		HideWindow:    true,
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", 0, fmt.Errorf("conpty spawn: stdout pipe: %w", err)
	}
	// Stderr is discarded; pty-host writes diagnostics there but we don't need them.
	cmd.Stderr = io.Discard

	if err := cmd.Start(); err != nil {
		return "", 0, fmt.Errorf("conpty spawn: start: %w", err)
	}

	// Read READY line with a timeout.
	readyC := make(chan struct {
		addr string
		pid  int
		err  error
	}, 1)

	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			m := readyRE.FindStringSubmatch(line)
			if m != nil {
				pid, _ := strconv.Atoi(m[1])
				port, _ := strconv.Atoi(m[2])
				readyC <- struct {
					addr string
					pid  int
					err  error
				}{"127.0.0.1:" + strconv.Itoa(port), pid, nil}
				return
			}
		}
		readyC <- struct {
			addr string
			pid  int
			err  error
		}{"", 0, fmt.Errorf("conpty spawn: pty-host exited without printing READY")}
	}()

	timer := time.NewTimer(spawnReadyTimeout)
	defer timer.Stop()

	select {
	case r := <-readyC:
		if r.err != nil {
			_ = cmd.Process.Kill()
			return "", 0, r.err
		}
		// Unref: detach stdout so the child is not blocked, then release reference
		// so our process can exit while the child keeps running.
		stdout.Close()
		cmd.Process.Release() // nolint: errcheck - best-effort detach
		return r.addr, cmd.Process.Pid, nil
	case <-timer.C:
		_ = cmd.Process.Kill()
		return "", 0, fmt.Errorf("conpty spawn: pty-host startup timeout (%s)", spawnReadyTimeout)
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		return "", 0, ctx.Err()
	}
}
