package cli

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// reviewRun mirrors the daemon's domain.ReviewRun for the CLI client.
type reviewRun struct {
	ID        string    `json:"id"`
	SessionID string    `json:"sessionId"`
	Harness   string    `json:"harness"`
	PRURL     string    `json:"prUrl"`
	TargetSHA string    `json:"targetSha"`
	Status    string    `json:"status"`
	Verdict   string    `json:"verdict"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"createdAt"`
}

// reviewRunResponse mirrors controllers.ReviewRunResponse.
type reviewRunResponse struct {
	Review           reviewRun `json:"review"`
	ReviewerHandleID string    `json:"reviewerHandleId"`
}

// submitReviewRequest mirrors controllers.SubmitReviewInput.
type submitReviewRequest struct {
	RunID   string `json:"runId"`
	Verdict string `json:"verdict"`
	Body    string `json:"body"`
}

type reviewSubmitOptions struct {
	session string
	runID   string
	verdict string
	body    string
}

func newReviewCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "review",
		Short: "Manage AO code reviews of a worker's PR",
	}
	cmd.AddCommand(newReviewSubmitCommand(ctx))
	return cmd
}

func newReviewSubmitCommand(ctx *commandContext) *cobra.Command {
	var opts reviewSubmitOptions
	cmd := &cobra.Command{
		Use:   "submit [worker-session-id]",
		Short: "Record a reviewer's result for a worker's PR",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.submitReview(cmd, args, opts)
		},
	}
	cmd.Flags().StringVar(&opts.session, "session", "", "Worker session id (or pass it as the positional argument)")
	cmd.Flags().StringVar(&opts.runID, "run", "", "Review run id (required)")
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
		return usageError{errors.New("usage: worker session id is required (positional or --session)")}
	}
	runID := strings.TrimSpace(opts.runID)
	if runID == "" {
		return usageError{errors.New("usage: --run is required")}
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
	path := "sessions/" + url.PathEscape(session) + "/reviews/submit"
	var res reviewRunResponse
	if err := c.postJSON(cmd.Context(), path, submitReviewRequest{RunID: runID, Verdict: verdict, Body: body}, &res); err != nil {
		return err
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "recorded %s review for %s\n", res.Review.Verdict, session)
	return err
}
