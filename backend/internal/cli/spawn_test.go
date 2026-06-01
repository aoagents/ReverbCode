package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/runfile"
)

// spawnServer wires up an httptest server, writes a runfile pointing at it, and
// returns the captured request body slot the caller assertions can read.
func spawnServer(t *testing.T, status int, respBody string) (*httptest.Server, *string) {
	t.Helper()
	var captured string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/sessions" && r.Method == http.MethodPost {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read req body: %v", err)
			}
			captured = string(body)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = io.WriteString(w, respBody)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &captured
}

func writeRunFileFor(t *testing.T, cfg testConfig, srv *httptest.Server) {
	t.Helper()
	port := serverPort(t, srv.URL)
	if err := runfile.Write(cfg.runFile, runfile.Info{
		PID:       os.Getpid(),
		Port:      port,
		StartedAt: time.Unix(100, 0).UTC(),
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSpawn_Success(t *testing.T) {
	cfg := setConfigEnv(t)
	resp := `{"sessionId":"demo-1","workspacePath":"/tmp/demo-1","runtimeHandle":"zellij-demo-1"}`
	srv, captured := spawnServer(t, http.StatusCreated, resp)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "spawn", "--project", "demo", "--prompt", "do the thing", "--agent", "claude-code")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, "Spawned session demo-1 in /tmp/demo-1") {
		t.Fatalf("stdout missing spawn line:\n%s", out)
	}
	if !strings.Contains(out, "Attach: zellij attach zellij-demo-1") {
		t.Fatalf("stdout missing attach line:\n%s", out)
	}

	var req struct {
		ProjectID string `json:"projectId"`
		Prompt    string `json:"prompt"`
		Agent     string `json:"agent"`
	}
	if err := json.Unmarshal([]byte(*captured), &req); err != nil {
		t.Fatalf("decode captured req: %v\nbody=%s", err, *captured)
	}
	if req.ProjectID != "demo" || req.Prompt != "do the thing" || req.Agent != "claude-code" {
		t.Fatalf("captured payload = %#v", req)
	}
}

func TestSpawn_DefaultsAgent(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, captured := spawnServer(t, http.StatusCreated,
		`{"sessionId":"demo-1","workspacePath":"/tmp/demo-1","runtimeHandle":"zellij-demo-1"}`)
	writeRunFileFor(t, cfg, srv)

	_, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "spawn", "--project", "demo", "--prompt", "x")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(*captured, `"agent":"claude-code"`) {
		t.Fatalf("agent default not sent: %s", *captured)
	}
}

func TestSpawn_EmptyPromptIsUsageError(t *testing.T) {
	setConfigEnv(t)
	_, _, err := executeCLI(t, Deps{}, "spawn", "--project", "demo", "--prompt", "   ")
	if err == nil {
		t.Fatal("expected usage error for empty prompt")
	}
	if got := ExitCode(err); got != 2 {
		t.Fatalf("exit code = %d, want 2", got)
	}
	if !strings.Contains(err.Error(), "--prompt is required") {
		t.Fatalf("error missing usage message: %v", err)
	}
}

func TestSpawn_MissingProjectIsUsageError(t *testing.T) {
	setConfigEnv(t)
	_, _, err := executeCLI(t, Deps{}, "spawn", "--prompt", "x")
	if err == nil {
		t.Fatal("expected usage error for missing project")
	}
	if got := ExitCode(err); got != 2 {
		t.Fatalf("exit code = %d, want 2", got)
	}
}

func TestSpawn_ServerBadRequestExits1(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := spawnServer(t, http.StatusBadRequest,
		`{"error":"bad_request","code":"PROMPT_REQUIRED","message":"prompt is required"}`)
	writeRunFileFor(t, cfg, srv)

	_, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "spawn", "--project", "demo", "--prompt", "x")
	if err == nil {
		t.Fatal("expected runtime error from 400")
	}
	if got := ExitCode(err); got != 1 {
		t.Fatalf("exit code = %d, want 1", got)
	}
	if !strings.Contains(err.Error(), "bad_request") && !strings.Contains(errOut, "bad_request") {
		t.Fatalf("error did not include server kind: %v\nstderr=%s", err, errOut)
	}
}

func TestSpawn_ServerNotFoundExits1(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := spawnServer(t, http.StatusNotFound,
		`{"error":"not_found","code":"PROJECT_NOT_FOUND","message":"Unknown project"}`)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "spawn", "--project", "missing", "--prompt", "x")
	if err == nil {
		t.Fatal("expected runtime error from 404")
	}
	if got := ExitCode(err); got != 1 {
		t.Fatalf("exit code = %d, want 1", got)
	}
}

func TestSpawn_ServerInternalErrorExits1(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := spawnServer(t, http.StatusInternalServerError,
		`{"error":"internal","code":"SPAWN_FAILED","message":"Failed to spawn session"}`)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "spawn", "--project", "demo", "--prompt", "x")
	if err == nil {
		t.Fatal("expected runtime error from 500")
	}
	if got := ExitCode(err); got != 1 {
		t.Fatalf("exit code = %d, want 1", got)
	}
}

func TestSpawn_DaemonNotRunningExits1(t *testing.T) {
	setConfigEnv(t)
	// No runfile: daemon is stopped.
	_, _, err := executeCLI(t, Deps{}, "spawn", "--project", "demo", "--prompt", "x")
	if err == nil {
		t.Fatal("expected error when daemon is not running")
	}
	if got := ExitCode(err); got != 1 {
		t.Fatalf("exit code = %d, want 1", got)
	}
}

func TestSpawn_SessionsDisabledExits1(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := spawnServer(t, http.StatusServiceUnavailable,
		`{"error":"sessions_disabled","code":"SESSIONS_DISABLED","message":"Session Manager is not wired in this daemon"}`)
	writeRunFileFor(t, cfg, srv)

	_, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "spawn", "--project", "demo", "--prompt", "x")
	if err == nil {
		t.Fatal("expected error from 503")
	}
	if got := ExitCode(err); got != 1 {
		t.Fatalf("exit code = %d, want 1", got)
	}
	if !strings.Contains(err.Error(), "sessions_disabled") && !strings.Contains(errOut, "sessions_disabled") {
		t.Fatalf("error did not include sessions_disabled: %v\nstderr=%s", err, errOut)
	}
}

// Sanity helper: ensure the formatted spawn message is stable.
func TestSpawn_StdoutShape(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := spawnServer(t, http.StatusCreated, fmt.Sprintf(
		`{"sessionId":%q,"workspacePath":%q,"runtimeHandle":%q}`,
		"proj-7", "/tmp/proj-7", "zellij-proj-7"))
	writeRunFileFor(t, cfg, srv)

	out, _, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "spawn", "--project", "proj", "--prompt", "go")
	if err != nil {
		t.Fatal(err)
	}
	want := "Spawned session proj-7 in /tmp/proj-7\nAttach: zellij attach zellij-proj-7\n"
	if out != want {
		t.Fatalf("stdout mismatch:\n got  %q\n want %q", out, want)
	}
}
