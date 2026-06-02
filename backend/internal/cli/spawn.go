package cli

import (
	"fmt"

	"github.com/spf13/cobra"
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
			_, err := fmt.Fprintf(cmd.OutOrStdout(),
				"spawned session %s (%s)\nattach with: zellij attach %s   (or `zellij list-sessions`)\n",
				res.Session.ID, res.Session.Status, res.Session.ID)
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
