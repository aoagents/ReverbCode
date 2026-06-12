package cli

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

// reviewRun mirrors the daemon's domain.ReviewRun for the CLI client.
type reviewRun struct {
	ID        string    `json:"id"`
	SessionID string    `json:"sessionId"`
	Harness   string    `json:"harness"`
	PRURL     string    `json:"prUrl"`
	Status    string    `json:"status"`
	Verdict   string    `json:"verdict"`
	Iteration int       `json:"iteration"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// reviewRunResponse mirrors controllers.ReviewRunResponse.
type reviewRunResponse struct {
	Review reviewRun `json:"review"`
}

// listReviewsResponse mirrors controllers.ListReviewsResponse.
type listReviewsResponse struct {
	Reviews []reviewRun `json:"reviews"`
}

// submitReviewRequest mirrors controllers.SubmitReviewInput.
type submitReviewRequest struct {
	Verdict string `json:"verdict"`
	Body    string `json:"body"`
}

type reviewSubmitOptions struct {
	session string
	verdict string
	body    string
}

func newReviewCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "review",
		Short: "Trigger and manage AO code reviews of a worker's PR",
	}
	cmd.AddCommand(newReviewTriggerCommand(ctx))
	cmd.AddCommand(newReviewSubmitCommand(ctx))
	cmd.AddCommand(newReviewListCommand(ctx))
	return cmd
}

func newReviewTriggerCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "trigger <worker-session-id>",
		Short: "Trigger a code review of a worker's PR",
		Args:  exactSessionArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			session := strings.TrimSpace(args[0])
			var res reviewRunResponse
			if err := ctx.postJSON(cmd.Context(), reviewPath(session, "trigger"), nil, &res); err != nil {
				return err
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "triggered review %s for %s (iteration %d, %s)\n",
				res.Review.ID, session, res.Review.Iteration, res.Review.Harness)
			return err
		},
	}
}

func newReviewListCommand(ctx *commandContext) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list <worker-session-id>",
		Short: "List a worker's code-review runs",
		Args:  exactSessionArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			session := strings.TrimSpace(args[0])
			var res listReviewsResponse
			if err := ctx.getJSON(cmd.Context(), reviewPath(session, ""), &res); err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), res)
			}
			return writeReviewList(cmd, res.Reviews)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output review runs as JSON")
	return cmd
}

func newReviewSubmitCommand(ctx *commandContext) *cobra.Command {
	var opts reviewSubmitOptions
	cmd := &cobra.Command{
		Use:   "submit [worker-session-id]",
		Short: "Submit a reviewer's result for a worker's PR",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.submitReview(cmd, args, opts)
		},
	}
	cmd.Flags().StringVar(&opts.session, "session", "", "Worker session id (defaults to $AO_REVIEW_WORKER)")
	cmd.Flags().StringVar(&opts.verdict, "verdict", "", "Review verdict: approved or changes_requested (required)")
	cmd.Flags().StringVar(&opts.body, "body", "", "Path to a Markdown file with the review body")
	return cmd
}

func (c *commandContext) submitReview(cmd *cobra.Command, args []string, opts reviewSubmitOptions) error {
	session := strings.TrimSpace(opts.session)
	if len(args) == 1 {
		session = strings.TrimSpace(args[0])
	}
	if session == "" {
		session = strings.TrimSpace(os.Getenv("AO_REVIEW_WORKER"))
	}
	if session == "" {
		return usageError{errors.New("usage: worker session id is required (positional, --session, or $AO_REVIEW_WORKER)")}
	}
	verdict := strings.TrimSpace(opts.verdict)
	if verdict == "" {
		return usageError{errors.New("usage: --verdict is required (approved or changes_requested)")}
	}
	var body string
	if path := strings.TrimSpace(opts.body); path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return usageError{fmt.Errorf("read body file: %w", err)}
		}
		body = string(raw)
	}
	var res reviewRunResponse
	if err := c.postJSON(cmd.Context(), reviewPath(session, "submit"), submitReviewRequest{Verdict: verdict, Body: body}, &res); err != nil {
		return err
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "submitted %s review for %s\n", res.Review.Verdict, session)
	return err
}

func reviewPath(session, action string) string {
	base := "sessions/" + url.PathEscape(session) + "/reviews"
	if action == "" {
		return base
	}
	return base + "/" + action
}

func exactSessionArg(cmd *cobra.Command, args []string) error {
	if err := cobra.ExactArgs(1)(cmd, args); err != nil {
		return usageError{err}
	}
	if strings.TrimSpace(args[0]) == "" {
		return usageError{errors.New("usage: worker session id is required")}
	}
	return nil
}

func writeReviewList(cmd *cobra.Command, runs []reviewRun) error {
	out := cmd.OutOrStdout()
	if len(runs) == 0 {
		_, err := fmt.Fprintln(out, "No reviews yet. Run `ao review trigger <worker-session-id>` to start one.")
		return err
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "ITER\tSTATUS\tVERDICT\tHARNESS\tPR"); err != nil {
		return err
	}
	for _, r := range runs {
		verdict := r.Verdict
		if verdict == "" {
			verdict = "-"
		}
		if _, err := fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\n", r.Iteration, r.Status, verdict, r.Harness, r.PRURL); err != nil {
			return err
		}
	}
	return tw.Flush()
}
