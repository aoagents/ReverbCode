package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/runfile"
)

// commandTimeout bounds a mutating daemon call. Spawns do real work (git
// worktree add, zellij launch, hook install), so it is generous compared to the
// status probe timeout.
const commandTimeout = 2 * time.Minute

// apiError is the subset of the daemon's JSON error envelope the CLI surfaces.
type apiError struct {
	Message string `json:"message"`
	Code    string `json:"code"`
}

// postJSON sends body as JSON to POST /api/v1/<path> on the running daemon and
// decodes a 2xx response into out (out may be nil). A non-2xx response becomes
// an error built from the API error envelope, and a missing daemon yields a
// clear "not running" message rather than a connection-refused dump.
func (c *commandContext) postJSON(ctx context.Context, path string, body, out any) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	info, err := runfile.Read(cfg.RunFilePath)
	if err != nil {
		return err
	}
	if info == nil {
		return fmt.Errorf("AO daemon is not running — start it with `ao start`")
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("http://%s:%d/api/v1/%s", config.LoopbackHost, info.Port, path)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	// A dedicated client: mutating calls need far more headroom than the 2s
	// status-probe client in Deps.
	resp, err := (&http.Client{Timeout: commandTimeout}).Do(req)
	if err != nil {
		return fmt.Errorf("call daemon: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var e apiError
		_ = json.NewDecoder(resp.Body).Decode(&e)
		switch {
		case e.Message != "" && e.Code != "":
			return fmt.Errorf("%s (%s)", e.Message, e.Code)
		case e.Message != "":
			return fmt.Errorf("%s", e.Message)
		default:
			return fmt.Errorf("daemon returned HTTP %d", resp.StatusCode)
		}
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
