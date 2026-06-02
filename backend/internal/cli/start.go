package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/runfile"
)

const defaultStartTimeout = 10 * time.Second

type startOptions struct {
	timeout time.Duration
	logFile string
	json    bool
}

func newStartCommand(ctx *commandContext) *cobra.Command {
	opts := startOptions{timeout: defaultStartTimeout}
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the AO daemon",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := ctx.startDaemon(cmd.Context(), opts)
			if err != nil {
				return err
			}
			if opts.json {
				return writeJSON(cmd.OutOrStdout(), st)
			}
			if st.State == stateReady {
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "AO daemon ready (pid %d, port %d)\n", st.PID, st.Port)
				return err
			}
			return writeStatus(cmd, st)
		},
	}
	cmd.Flags().DurationVar(&opts.timeout, "timeout", defaultStartTimeout, "How long to wait for daemon readiness")
	cmd.Flags().StringVar(&opts.logFile, "log-file", "", "Daemon log file path")
	cmd.Flags().BoolVar(&opts.json, "json", false, "Output start result as JSON")
	return cmd
}

func (c *commandContext) startDaemon(ctx context.Context, opts startOptions) (daemonStatus, error) {
	cfg, err := config.Load()
	if err != nil {
		return daemonStatus{}, err
	}

	st, err := c.inspectDaemon(ctx)
	if err != nil {
		return daemonStatus{}, err
	}
	if st.State == stateReady {
		return st, nil
	}
	if st.State != stateStopped && st.State != stateStale {
		ready, waitErr := c.waitForReady(ctx, opts.timeout)
		if waitErr == nil {
			return ready, nil
		}
		return daemonStatus{}, fmt.Errorf("daemon process exists but did not become ready: %w", waitErr)
	}
	if st.State == stateStale {
		if err := runfile.Remove(cfg.RunFilePath); err != nil {
			return daemonStatus{}, err
		}
	}

	exe, err := c.deps.Executable()
	if err != nil {
		return daemonStatus{}, fmt.Errorf("resolve executable: %w", err)
	}

	logPath := opts.logFile
	if logPath == "" {
		logPath = filepath.Join(filepath.Dir(cfg.RunFilePath), "daemon.log")
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o750); err != nil {
		return daemonStatus{}, fmt.Errorf("create log dir: %w", err)
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return daemonStatus{}, fmt.Errorf("open daemon log: %w", err)
	}
	defer func() { _ = logFile.Close() }()

	if err := c.deps.StartProcess(processStartConfig{
		Path:   exe,
		Args:   []string{"daemon"},
		Env:    os.Environ(),
		Stdout: logFile,
		Stderr: logFile,
	}); err != nil {
		return daemonStatus{}, fmt.Errorf("start daemon: %w", err)
	}

	ready, err := c.waitForReady(ctx, opts.timeout)
	if err != nil {
		return daemonStatus{}, fmt.Errorf("%w; see daemon log %s", err, logPath)
	}
	return ready, nil
}

func (c *commandContext) waitForReady(ctx context.Context, timeout time.Duration) (daemonStatus, error) {
	if timeout <= 0 {
		timeout = defaultStartTimeout
	}
	deadline := c.deps.Now().Add(timeout)
	var last daemonStatus
	var lastErr error

	for {
		select {
		case <-ctx.Done():
			return daemonStatus{}, ctx.Err()
		default:
		}

		st, err := c.inspectDaemon(ctx)
		if err != nil {
			lastErr = err
		} else {
			last = st
			if st.State == stateReady {
				return st, nil
			}
		}

		if !c.deps.Now().Before(deadline) {
			if lastErr != nil {
				return daemonStatus{}, fmt.Errorf("daemon did not become ready within %s: %w", timeout, lastErr)
			}
			return daemonStatus{}, fmt.Errorf("daemon did not become ready within %s (last state: %s)", timeout, last.State)
		}
		c.deps.Sleep(100 * time.Millisecond)
	}
}
