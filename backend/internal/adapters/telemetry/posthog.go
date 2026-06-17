package telemetry

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const postHogBufferSize = 128

type postHogClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// PostHogSink exports allowlisted telemetry events to PostHog.
type PostHogSink struct {
	apiKey     string
	host       string
	distinctID string
	client     postHogClient
	log        *slog.Logger
	ch         chan ports.TelemetryEvent
	wg         sync.WaitGroup
	closeOnce  sync.Once
}

func NewPostHogSink(dataDir, apiKey, host string, client postHogClient, log *slog.Logger) (*PostHogSink, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("posthog api key is required")
	}
	if strings.TrimSpace(host) == "" {
		return nil, fmt.Errorf("posthog host is required")
	}
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	distinctID, err := loadOrCreateInstallID(dataDir)
	if err != nil {
		return nil, err
	}
	s := &PostHogSink{
		apiKey:     apiKey,
		host:       strings.TrimRight(host, "/"),
		distinctID: distinctID,
		client:     client,
		log:        telemetryLogger(log),
		ch:         make(chan ports.TelemetryEvent, postHogBufferSize),
	}
	s.wg.Add(1)
	go s.loop()
	return s, nil
}

func (s *PostHogSink) Emit(_ context.Context, ev ports.TelemetryEvent) {
	select {
	case s.ch <- ev:
	default:
		s.log.Warn("telemetry posthog sink buffer full; dropping event", "name", ev.Name, "source", ev.Source)
	}
}

func (s *PostHogSink) Close(ctx context.Context) error {
	s.closeOnce.Do(func() { close(s.ch) })
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.wg.Wait()
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

func (s *PostHogSink) loop() {
	defer s.wg.Done()
	for ev := range s.ch {
		s.send(ev)
	}
}

func (s *PostHogSink) send(ev ports.TelemetryEvent) {
	body := map[string]any{
		"api_key":     s.apiKey,
		"event":       ev.Name,
		"distinct_id": s.distinctID,
		"properties":  s.properties(ev),
		"timestamp":   ev.OccurredAt.UTC().Format(time.RFC3339Nano),
	}
	payload, err := json.Marshal(body)
	if err != nil {
		s.log.Warn("telemetry posthog payload marshal failed", "name", ev.Name, "error", err)
		return
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, s.host+"/capture/", bytes.NewReader(payload))
	if err != nil {
		s.log.Warn("telemetry posthog request build failed", "name", ev.Name, "error", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		s.log.Warn("telemetry posthog export failed", "name", ev.Name, "error", err)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		s.log.Warn("telemetry posthog rejected event", "name", ev.Name, "status", resp.StatusCode, "body", strings.TrimSpace(string(b)))
	}
}

func (s *PostHogSink) properties(ev ports.TelemetryEvent) map[string]any {
	props := map[string]any{
		"source": ev.Source,
		"level":  string(ev.Level),
	}
	if ev.RequestID != "" {
		props["request_id"] = ev.RequestID
	}
	if ev.ProjectID != nil {
		props["project_id_hash"] = sha256String(string(*ev.ProjectID))
	}
	if ev.SessionID != nil {
		props["session_id_hash"] = sha256String(string(*ev.SessionID))
	}
	for k, v := range ev.Payload {
		props[k] = v
	}
	return props
}

func loadOrCreateInstallID(dataDir string) (string, error) {
	path := filepath.Join(dataDir, "telemetry_install_id")
	if b, err := os.ReadFile(path); err == nil {
		if id := strings.TrimSpace(string(b)); id != "" {
			return id, nil
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("read telemetry install id: %w", err)
	}
	id := "ins_" + uuid.NewString()
	if err := os.WriteFile(path, []byte(id+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("write telemetry install id: %w", err)
	}
	return id, nil
}

func sha256String(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func telemetryLogger(log *slog.Logger) *slog.Logger {
	if log != nil {
		return log
	}
	return slog.Default()
}
