package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/zellij"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
)

type doctorLevel string

const (
	doctorPass doctorLevel = "PASS"
	doctorWarn doctorLevel = "WARN"
	doctorFail doctorLevel = "FAIL"
)

type doctorCheck struct {
	Level   doctorLevel `json:"level"`
	Name    string      `json:"name"`
	Message string      `json:"message"`
}

type doctorReport struct {
	OK       bool          `json:"ok"`
	Failures int           `json:"failures"`
	Checks   []doctorCheck `json:"checks"`
}

func newDoctorCommand(ctx *commandContext) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Run local AO health checks",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			checks := ctx.runDoctor(cmd.Context())
			failures := 0
			for _, check := range checks {
				if check.Level == doctorFail {
					failures++
				}
			}

			if asJSON {
				if err := writeJSON(cmd.OutOrStdout(), doctorReport{
					OK: failures == 0, Failures: failures, Checks: checks,
				}); err != nil {
					return err
				}
			} else {
				for _, check := range checks {
					if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s %s: %s\n", check.Level, check.Name, check.Message); err != nil {
						return err
					}
				}
			}

			if failures > 0 {
				return fmt.Errorf("doctor found %d failing check(s)", failures)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output health checks as JSON")
	return cmd
}

func (c *commandContext) runDoctor(ctx context.Context) []doctorCheck {
	checks := []doctorCheck{}

	cfg, err := config.Load()
	if err != nil {
		return append(checks, doctorCheck{Level: doctorFail, Name: "config", Message: err.Error()})
	}
	checks = append(checks, doctorCheck{
		Level: doctorPass, Name: "config",
		Message: fmt.Sprintf("runFile=%s dataDir=%s port=%d", cfg.RunFilePath, cfg.DataDir, cfg.Port),
	})

	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		checks = append(checks, doctorCheck{Level: doctorFail, Name: "data-dir", Message: err.Error()})
	} else {
		checks = append(checks, doctorCheck{Level: doctorPass, Name: "data-dir", Message: cfg.DataDir})
	}

	checks = append(checks, checkStore(cfg.DataDir))

	st, err := c.inspectDaemon(ctx)
	if err != nil {
		checks = append(checks, doctorCheck{Level: doctorFail, Name: "daemon", Message: err.Error()})
	} else {
		level := doctorPass
		switch st.State {
		case stateStale, stateNotReady:
			level = doctorWarn
		case stateUnhealthy:
			level = doctorFail
		}
		msg := string(st.State)
		if st.PID != 0 {
			msg = fmt.Sprintf("%s pid=%d port=%d", msg, st.PID, st.Port)
		}
		if st.Error != "" {
			msg += " (" + st.Error + ")"
		}
		checks = append(checks, doctorCheck{Level: level, Name: "daemon", Message: msg})
	}

	checks = append(checks,
		c.checkTool("git", true),
		c.checkZellij(ctx),
	)
	return checks
}

// checkStore inspects the SQLite store WITHOUT opening or migrating it. The
// daemon is the sole writer and migrator of the database (architecture.md §7);
// the CLI must never run migrations or open a second writer against a database
// a live daemon may already own. Migrations are validated by the daemon at
// startup and surfaced through /readyz, so doctor only confirms whether the
// database file exists yet.
func checkStore(dataDir string) doctorCheck {
	dbPath := filepath.Join(dataDir, "ao.db")
	info, err := os.Stat(dbPath)
	switch {
	case err == nil:
		return doctorCheck{
			Level: doctorPass, Name: "sqlite",
			Message: fmt.Sprintf("%s (%d bytes); migrations are applied by the daemon at startup", dbPath, info.Size()),
		}
	case errors.Is(err, fs.ErrNotExist):
		return doctorCheck{
			Level: doctorWarn, Name: "sqlite",
			Message: "database not created yet; run `ao start` to initialize and migrate it",
		}
	default:
		return doctorCheck{Level: doctorFail, Name: "sqlite", Message: err.Error()}
	}
}

func (c *commandContext) checkZellij(ctx context.Context) doctorCheck {
	path, err := c.deps.LookPath("zellij")
	if err != nil {
		return doctorCheck{Level: doctorWarn, Name: "zellij", Message: "not found in PATH"}
	}
	reqCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	out, err := c.deps.CommandOutput(reqCtx, path, "--version")
	if err != nil {
		return doctorCheck{Level: doctorFail, Name: "zellij", Message: fmt.Sprintf("%s: %v", path, err)}
	}
	version, err := zellij.CheckVersionOutput(string(out))
	if err != nil {
		return doctorCheck{Level: doctorFail, Name: "zellij", Message: fmt.Sprintf("%s: %v", path, err)}
	}
	return doctorCheck{Level: doctorPass, Name: "zellij", Message: fmt.Sprintf("%s (%s)", path, version)}
}

func (c *commandContext) checkTool(name string, required bool) doctorCheck {
	path, err := c.deps.LookPath(name)
	if err == nil {
		return doctorCheck{Level: doctorPass, Name: name, Message: path}
	}
	if required {
		return doctorCheck{Level: doctorFail, Name: name, Message: "not found in PATH"}
	}
	return doctorCheck{Level: doctorWarn, Name: name, Message: "not found in PATH"}
}
