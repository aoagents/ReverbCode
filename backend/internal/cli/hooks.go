package cli

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/activitydispatch"
)

// setActivityAPIRequest mirrors the daemon's SetActivityRequest body for
// POST /api/v1/sessions/{id}/activity. The CLI keeps its own copy so it need
// not import httpd.
type setActivityAPIRequest struct {
	State string `json:"state"`
}

// newHooksCommand builds the hidden `ao hooks <agent> <event>` command that
// agent CLIs invoke from their workspace-local hook config. It reads the native
// hook payload from stdin and the AO session id from AO_SESSION_ID, derives an
// activity state for the event, and reports it to the daemon.
//
// It is best-effort by design: a hook must never break the user's agent, so a
// non-AO session (no AO_SESSION_ID), an event that carries no activity signal,
// or an unreachable daemon all exit 0 rather than erroring.
func newHooksCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:    "hooks <agent> <event>",
		Short:  "Receive an agent hook callback (internal)",
		Hidden: true,
		Args:   cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.runHook(cmd.Context(), args[0], args[1])
		},
	}
}

func (c *commandContext) runHook(ctx context.Context, agent, event string) error {
	sessionID := strings.TrimSpace(os.Getenv("AO_SESSION_ID"))
	if sessionID == "" {
		// Not an AO-managed session. Return before reading stdin so a manual
		// invocation without a piped payload can't block on EOF.
		return nil
	}
	payload, err := io.ReadAll(c.deps.In)
	if err != nil {
		// Surface read errors to stderr for parity with the daemon-error path,
		// but keep the empty payload and exit 0: a failed hook must not break
		// the agent. The deriver tolerates an empty payload.
		_, _ = fmt.Fprintf(c.deps.Err, "ao hooks %s %s: read stdin: %v\n", agent, event, err)
	}

	state, ok := activitydispatch.Derive(agent, event, payload)
	if !ok {
		// Unknown agent, or an event that carries no activity signal: report nothing.
		return nil
	}

	path := "sessions/" + url.PathEscape(sessionID) + "/activity"
	if err := c.postJSON(ctx, path, setActivityAPIRequest{State: string(state)}, nil); err != nil {
		// Report to stderr (the agent's hook runner captures it) for diagnosis,
		// but exit 0: a failed activity report must not disrupt the agent.
		_, _ = fmt.Fprintf(c.deps.Err, "ao hooks %s %s: %v\n", agent, event, err)
	}
	return nil
}
