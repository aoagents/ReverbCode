package cli

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

type sendOptions struct {
	session string
	message string
}

// sendAPIRequest mirrors the daemon's SendSessionMessageRequest body for
// POST /api/v1/sessions/{id}/send. The CLI keeps its own copy so it need not
// import httpd.
type sendAPIRequest struct {
	Message string `json:"message"`
}

func newSendCommand(ctx *commandContext) *cobra.Command {
	var opts sendOptions
	cmd := &cobra.Command{
		Use:   "send",
		Short: "Send a message to a running agent session",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return ctx.sendMessage(cmd.Context(), opts)
		},
	}
	cmd.Flags().StringVar(&opts.session, "session", "", "Session id (required)")
	cmd.Flags().StringVar(&opts.message, "message", "", "Message body (required)")
	return cmd
}

func (c *commandContext) sendMessage(ctx context.Context, opts sendOptions) error {
	if strings.TrimSpace(opts.message) == "" {
		return usageError{errors.New("usage: --message is required")}
	}
	message := opts.message
	if sender := strings.TrimSpace(os.Getenv("AO_SESSION_ID")); sender != "" {
		message = "[from " + sender + "] " + message
	}
	session := strings.TrimSpace(opts.session)
	if session == "" {
		return usageError{errors.New("usage: --session is required")}
	}

	// PathEscape: session ids are already "-"/digit safe, but may later come
	// from sanitized issue refs; keep the URL well-formed regardless.
	path := "sessions/" + url.PathEscape(session) + "/send"
	return c.postJSON(ctx, path, sendAPIRequest{Message: message}, nil)
}
