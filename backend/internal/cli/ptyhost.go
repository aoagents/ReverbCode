package cli

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/conpty"
)

// newPtyHostCommand registers the "ao pty-host" hidden subcommand that the
// conpty runtime spawns on Windows to host a ConPTY session over loopback TCP.
// DisableFlagParsing ensures agent shell args with leading dashes are not
// consumed by cobra before being passed to RunHost.
func newPtyHostCommand() *cobra.Command {
	return &cobra.Command{
		Use:                "pty-host",
		Short:              "Run a ConPTY pty-host process (internal)",
		Hidden:             true,
		DisableFlagParsing: true,
		RunE: func(_ *cobra.Command, args []string) error {
			code := conpty.RunHost(args, os.Stdout)
			if code != 0 {
				os.Exit(code)
			}
			return nil
		},
	}
}
