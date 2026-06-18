package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/agentlaunch"
)

func newLaunchCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:    "launch",
		Short:  "Launch an AO-managed agent process (internal)",
		Hidden: true,
		Args:   noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return ctx.launchAgent(cmd.Context())
		},
	}
}

func (c *commandContext) launchAgent(ctx context.Context) error {
	specPath := strings.TrimSpace(os.Getenv(agentlaunch.EnvSpecPath))
	if specPath == "" {
		return errors.New("launch: AO_LAUNCH_SPEC is required")
	}
	spec, err := agentlaunch.ReadAndRemove(specPath)
	if err != nil {
		return fmt.Errorf("launch: %w", err)
	}

	cmd := exec.CommandContext(ctx, spec.Argv[0], spec.Argv[1:]...)
	cmd.Dir = spec.WorkspacePath
	cmd.Env = withoutLaunchSpecEnv(os.Environ())
	cmd.Stdin = c.deps.In
	cmd.Stdout = c.deps.Out
	cmd.Stderr = c.deps.Err
	return cmd.Run()
}

func withoutLaunchSpecEnv(env []string) []string {
	cleaned := env[:0]
	for _, pair := range env {
		key, _, ok := strings.Cut(pair, "=")
		if !ok {
			cleaned = append(cleaned, pair)
			continue
		}
		if envKeyEqual(key, agentlaunch.EnvSpecPath) {
			continue
		}
		cleaned = append(cleaned, pair)
	}
	return cleaned
}

func envKeyEqual(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
