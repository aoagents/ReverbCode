package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/legacyimport"
	"github.com/aoagents/agent-orchestrator/backend/internal/runfile"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
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

	// First-boot opt-in: before launching the daemon (so the import runs with the
	// store unlocked and the daemon as sole writer afterwards), offer to import a
	// legacy AO install. Declining or any import failure is non-fatal — the
	// daemon still starts and the user can run `ao import` later.
	c.maybeFirstBootImport(ctx, cfg)

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

// maybeFirstBootImport offers to import a legacy AO install the first time the
// daemon is started against an empty rewrite database. It is best-effort: every
// failure path degrades to "start the daemon fresh" so a broken or absent legacy
// store can never block startup. A non-interactive boot (Electron/headless)
// never auto-imports; it prints a one-line hint to run `ao import` explicitly.
func (c *commandContext) maybeFirstBootImport(ctx context.Context, cfg config.Config) {
	root := legacyimport.DefaultLegacyRootDir()
	if !legacyimport.HasLegacyData(root) {
		return
	}

	store, err := sqlite.Open(cfg.DataDir)
	if err != nil {
		return // the daemon will surface a real store error on its own open
	}
	defer func() { _ = store.Close() }()

	projects, err := store.ListProjects(ctx)
	if err != nil || len(projects) > 0 {
		// Already imported (or populated) — don't offer again.
		return
	}

	out := c.deps.Out
	if !stdinIsInteractive(c.deps.In) {
		_, _ = fmt.Fprintf(out, "Found existing AO projects at %s. Run `ao import` to bring them in.\n", root)
		return
	}

	ok, err := confirm(c.deps.In, out, "Found existing AO projects and sessions. Import them now?", true)
	if err != nil || !ok {
		_, _ = fmt.Fprintln(out, "Continuing fresh. Run `ao import` later to bring in your existing data.")
		return
	}

	rep, err := legacyimport.Run(ctx, store, legacyimport.Options{Root: root, DataDir: cfg.DataDir})
	if err != nil {
		_, _ = fmt.Fprintf(out, "Import failed: %v\nContinuing fresh; legacy data is untouched. Retry with `ao import`.\n", err)
		return
	}
	_ = writeImportSummary(out, rep)
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
