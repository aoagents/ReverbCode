package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/zellij"
)

type spawnOptions struct {
	project string
	harness string
	branch  string
	prompt  string
	issue   string
	rules   string
}

// spawnRequest mirrors the daemon's SpawnSessionRequest body for
// POST /api/v1/sessions. The CLI keeps its own copy so it need not import httpd.
type spawnRequest struct {
	ProjectID  string `json:"projectId"`
	IssueID    string `json:"issueId,omitempty"`
	Harness    string `json:"harness,omitempty"`
	Branch     string `json:"branch,omitempty"`
	Prompt     string `json:"prompt,omitempty"`
	AgentRules string `json:"agentRules,omitempty"`
}

type spawnResult struct {
	Session struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"session"`
}

func newSpawnCommand(ctx *commandContext) *cobra.Command {
	var opts spawnOptions
	cmd := &cobra.Command{
		Use:   "spawn",
		Short: "Spawn a worker agent session in a registered project",
		Long: "Spawn a worker agent session in a registered project.\n\n" +
			"The session runs the chosen agent (default: the daemon's AO_AGENT) in a\n" +
			"fresh git worktree. Register the project first with `ao project add`.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.project == "" {
				return usageError{fmt.Errorf("--project is required")}
			}
			req := spawnRequest{
				ProjectID:  opts.project,
				IssueID:    opts.issue,
				Harness:    opts.harness,
				Branch:     opts.branch,
				Prompt:     opts.prompt,
				AgentRules: opts.rules,
			}
			var res spawnResult
			if err := ctx.postJSON(cmd.Context(), "sessions", req, &res); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if _, err := fmt.Fprintf(out, "spawned session %s (%s)\n", res.Session.ID, res.Session.Status); err != nil {
				return err
			}
			// The daemon runs zellij under a short, non-default socket dir (see
			// zellij.DefaultSocketDir), so a plain `zellij attach` wouldn't find
			// the session — prefix the env so the hint is copy-pasteable. Use the
			// sanitised name zellij actually registers (zellij.SessionName): a long
			// session id maps to a different name than the raw id.
			attach := fmt.Sprintf("zellij attach %s", zellij.SessionName(res.Session.ID))
			if dir := zellij.DefaultSocketDir(); dir != "" {
				attach = fmt.Sprintf("ZELLIJ_SOCKET_DIR=%s %s", dir, attach)
			}
			_, err := fmt.Fprintf(out, "attach with: %s\n", attach)
			return err
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.project, "project", "", "Project id to spawn the session in (required)")
	f.StringVar(&opts.harness, "harness", "", "Agent harness: claude-code, codex, … (default: the daemon's AO_AGENT)")
	f.StringVar(&opts.branch, "branch", "", "Branch for the session worktree (default: ao/<session-id>)")
	f.StringVar(&opts.prompt, "prompt", "", "Initial prompt for the agent")
	f.StringVar(&opts.issue, "issue", "", "Issue id to associate with the session")
	f.StringVar(&opts.rules, "rules", "", "Agent rules appended to the prompt")
	return cmd
}
