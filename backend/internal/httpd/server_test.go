package httpd

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/runfile"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestHealthProbes(t *testing.T) {
	router := NewRouter(config.Config{}, discardLogger(), nil)
	srv := httptest.NewServer(router)
	defer srv.Close()

	client := &http.Client{Timeout: 2 * time.Second}
	for _, path := range []string{"/healthz", "/readyz"} {
		resp, err := client.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); ct != "application/json; charset=utf-8" {
			t.Errorf("GET %s Content-Type = %q, want JSON", path, ct)
		}
	}
}

// TestServerLifecycle exercises the full Run loop: bind an ephemeral port,
// publish running.json, serve a request, then cancel the context and confirm a
// clean shutdown that removes the handshake file.
func TestServerLifecycle(t *testing.T) {
	runPath := filepath.Join(t.TempDir(), "running.json")
	cfg := config.Config{
		Host:            "127.0.0.1",
		Port:            0, // let the OS pick a free port — no conflict with a real daemon
		ShutdownTimeout: 5 * time.Second,
		RunFilePath:     runPath,
	}

	srv, err := New(cfg, discardLogger(), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- srv.Run(ctx) }()

	// Wait for the handshake file to confirm the server is up.
	base := "http://" + srv.Addr().String()
	waitForHealth(t, base)

	info, err := runfile.Read(runPath)
	if err != nil {
		t.Fatalf("read run-file: %v", err)
	}
	if info == nil {
		t.Fatal("run-file not written while server running")
	}
	if info.Port == 0 {
		t.Error("run-file recorded port 0; want the actual bound port")
	}

	cancel()

	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run returned error on graceful shutdown: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after context cancel")
	}

	if after, _ := runfile.Read(runPath); after != nil {
		t.Error("run-file still present after shutdown; want it removed")
	}
}

func TestServerShutdownEndpoint(t *testing.T) {
	runPath := filepath.Join(t.TempDir(), "running.json")
	cfg := config.Config{
		Host:            "127.0.0.1",
		Port:            0,
		ShutdownTimeout: 5 * time.Second,
		RunFilePath:     runPath,
	}

	srv, err := New(cfg, discardLogger(), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	runErr := make(chan error, 1)
	go func() { runErr <- srv.Run(context.Background()) }()

	base := "http://" + srv.Addr().String()
	waitForHealth(t, base)

	resp, err := http.Post(base+"/shutdown", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /shutdown: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST /shutdown = %d, want 202", resp.StatusCode)
	}

	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run returned error on shutdown endpoint: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after shutdown endpoint")
	}

	if after, _ := runfile.Read(runPath); after != nil {
		t.Error("run-file still present after shutdown endpoint; want it removed")
	}
}

func waitForHealth(t *testing.T, base string) {
	t.Helper()
	// Per-request timeout so a stalled connect or hung handshake doesn't park
	// the test for the full Go test timeout; the outer deadline only bounds
	// the polling loop, not any single GET.
	client := &http.Client{Timeout: 500 * time.Millisecond}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(base + "/healthz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("server did not become healthy within timeout")
}

// TestNewFailsOnPortConflict confirms a second bind of the same port fails
// fast rather than silently sharing it.
func TestNewFailsOnPortConflict(t *testing.T) {
	cfg := config.Config{Host: "127.0.0.1", Port: 0, RunFilePath: filepath.Join(t.TempDir(), "r.json")}

	first, err := New(cfg, discardLogger(), nil)
	if err != nil {
		t.Fatalf("first New: %v", err)
	}
	defer first.listen.Close()

	// Re-bind the exact port the first server took.
	conflict := config.Config{Host: "127.0.0.1", Port: first.boundPort(), RunFilePath: cfg.RunFilePath}
	if _, err := New(conflict, discardLogger(), nil); err == nil {
		t.Fatal("New on an already-bound port = nil error, want bind failure")
	}
}
