package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/runfile"
)

type sendOptions struct {
	session string
	message string
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

type sendAPIRequest struct {
	Message string `json:"message"`
}

func (c *commandContext) sendMessage(ctx context.Context, opts sendOptions) error {
	message := strings.TrimSpace(opts.message)
	if message == "" {
		return usageError{errors.New("usage: --message is required")}
	}
	session := strings.TrimSpace(opts.session)
	if session == "" {
		return usageError{errors.New("usage: --session is required")}
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	info, err := runfile.Read(cfg.RunFilePath)
	if err != nil {
		return fmt.Errorf("read run-file: %w", err)
	}
	if info == nil {
		return errors.New("AO daemon is not running; start it with `ao start`")
	}

	body, err := json.Marshal(sendAPIRequest{Message: message})
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}

	// PathEscape: session ids include "-" and digits already, but they may also
	// come from sanitized issue refs in the future; keep the URL well-formed.
	endpoint := fmt.Sprintf("http://%s:%d/api/v1/sessions/%s/send",
		config.LoopbackHost, info.Port, url.PathEscape(session))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.deps.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("daemon request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	var apiErr apiError
	if jerr := json.Unmarshal(respBody, &apiErr); jerr == nil && apiErr.Kind != "" {
		return fmt.Errorf("%s: %s", apiErr.Kind, apiErr.Message)
	}
	return fmt.Errorf("daemon returned HTTP %d", resp.StatusCode)
}
