package cli

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/daemonmeta"
	"github.com/aoagents/agent-orchestrator/backend/internal/runfile"
)

func TestRootHelpDoesNotShowDaemon(t *testing.T) {
	out, _, err := executeCLI(t, Deps{}, "--help")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "\n  daemon") {
		t.Fatalf("hidden daemon command leaked into help:\n%s", out)
	}
	for _, want := range []string{"start", "stop", "status", "doctor", "completion", "version"} {
		if !strings.Contains(out, want) {
			t.Fatalf("help missing %q:\n%s", want, out)
		}
	}
}

func TestCommandsRejectUnexpectedArgs(t *testing.T) {
	for _, args := range [][]string{
		{"daemon", "extra"},
		{"start", "extra"},
		{"stop", "extra"},
		{"status", "extra"},
		{"doctor", "extra"},
		{"version", "extra"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			_, _, err := executeCLI(t, Deps{}, args...)
			if err == nil {
				t.Fatal("expected usage error")
			}
			if got := ExitCode(err); got != 2 {
				t.Fatalf("ExitCode(%v) = %d, want 2", err, got)
			}
		})
	}
}

func TestStatusStoppedJSON(t *testing.T) {
	setConfigEnv(t)

	out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return false }}, "status", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"state": "stopped"`) {
		t.Fatalf("status did not report stopped:\n%s", out)
	}
	if strings.Contains(out, "startedAt") {
		t.Fatalf("stopped JSON should omit startedAt:\n%s", out)
	}
}

func TestStartReturnsExistingReadyDaemon(t *testing.T) {
	cfg := setConfigEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			_, _ = fmt.Fprintf(w, `{"status":"ok","service":%q,"pid":%d}`, daemonmeta.ServiceName, os.Getpid())
		case "/readyz":
			_, _ = fmt.Fprintf(w, `{"status":"ready","service":%q,"pid":%d}`, daemonmeta.ServiceName, os.Getpid())
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	port := serverPort(t, srv.URL)
	if err := runfile.Write(cfg.runFile, runfile.Info{PID: os.Getpid(), Port: port, StartedAt: time.Unix(100, 0).UTC()}); err != nil {
		t.Fatal(err)
	}

	var started bool
	out, _, err := executeCLI(t, Deps{
		ProcessAlive: func(pid int) bool { return pid == os.Getpid() },
		StartProcess: func(processStartConfig) error {
			started = true
			return nil
		},
		Now: func() time.Time { return time.Unix(110, 0).UTC() },
	}, "start", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if started {
		t.Fatal("start should not spawn when daemon is already ready")
	}
	if !strings.Contains(out, `"state": "ready"`) {
		t.Fatalf("start did not report ready:\n%s", out)
	}
}

func TestStartClearsStaleRunFileBeforeSpawning(t *testing.T) {
	cfg := setConfigEnv(t)
	var spawned atomic.Bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !spawned.Load() {
			_, _ = fmt.Fprintf(w, `{"status":"ok","service":"not-ao","pid":4242}`)
			return
		}
		switch r.URL.Path {
		case "/healthz":
			_, _ = fmt.Fprintf(w, `{"status":"ok","service":%q,"pid":%d}`, daemonmeta.ServiceName, os.Getpid())
		case "/readyz":
			_, _ = fmt.Fprintf(w, `{"status":"ready","service":%q,"pid":%d}`, daemonmeta.ServiceName, os.Getpid())
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	port := serverPort(t, srv.URL)
	if err := runfile.Write(cfg.runFile, runfile.Info{PID: 4242, Port: port, StartedAt: time.Unix(100, 0).UTC()}); err != nil {
		t.Fatal(err)
	}

	out, _, err := executeCLI(t, Deps{
		ProcessAlive: func(pid int) bool { return pid == 4242 || pid == os.Getpid() },
		StartProcess: func(processStartConfig) error {
			info, err := runfile.Read(cfg.runFile)
			if err != nil {
				t.Fatal(err)
			}
			if info != nil {
				t.Fatalf("stale run-file was not removed before spawn: %#v", info)
			}
			spawned.Store(true)
			if err := runfile.Write(cfg.runFile, runfile.Info{PID: os.Getpid(), Port: port, StartedAt: time.Unix(110, 0).UTC()}); err != nil {
				t.Fatal(err)
			}
			return nil
		},
		Now: func() time.Time { return time.Unix(120, 0).UTC() },
	}, "start", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"state": "ready"`) {
		t.Fatalf("start did not report ready after clearing stale run-file:\n%s", out)
	}
}

func TestStopRemovesStaleRunFile(t *testing.T) {
	cfg := setConfigEnv(t)
	if err := runfile.Write(cfg.runFile, runfile.Info{PID: 999999, Port: 3001, StartedAt: time.Unix(100, 0).UTC()}); err != nil {
		t.Fatal(err)
	}

	out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return false }}, "stop", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"state": "stopped"`) {
		t.Fatalf("stop did not report stopped:\n%s", out)
	}
	info, err := runfile.Read(cfg.runFile)
	if err != nil {
		t.Fatal(err)
	}
	if info != nil {
		t.Fatalf("stale run-file was not removed: %#v", info)
	}
}

func TestStopDoesNotShutdownUnverifiedReusedPID(t *testing.T) {
	cfg := setConfigEnv(t)
	shutdownCalled := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "/readyz":
			_, _ = w.Write([]byte(`{"status":"ready"}`))
		case "/shutdown":
			shutdownCalled <- struct{}{}
			http.Error(w, "unexpected shutdown", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	if err := runfile.Write(cfg.runFile, runfile.Info{PID: 4242, Port: serverPort(t, srv.URL), StartedAt: time.Unix(100, 0).UTC()}); err != nil {
		t.Fatal(err)
	}

	out, _, err := executeCLI(t, Deps{
		ProcessAlive: func(pid int) bool { return pid == 4242 },
	}, "stop", "--json")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-shutdownCalled:
		t.Fatal("stop requested shutdown from a process whose health probe did not prove AO daemon ownership")
	default:
	}
	if !strings.Contains(out, `"state": "stopped"`) {
		t.Fatalf("stop did not report stopped:\n%s", out)
	}
	info, err := runfile.Read(cfg.runFile)
	if err != nil {
		t.Fatal(err)
	}
	if info != nil {
		t.Fatalf("unverified run-file was not removed: %#v", info)
	}
}

func TestStopUsesShutdownEndpoint(t *testing.T) {
	cfg := setConfigEnv(t)
	shutdownCalled := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			_, _ = fmt.Fprintf(w, `{"status":"ok","service":%q,"pid":%d}`, daemonmeta.ServiceName, os.Getpid())
		case "/readyz":
			_, _ = fmt.Fprintf(w, `{"status":"ready","service":%q,"pid":%d}`, daemonmeta.ServiceName, os.Getpid())
		case "/shutdown":
			if err := runfile.Remove(cfg.runFile); err != nil {
				t.Fatal(err)
			}
			shutdownCalled <- struct{}{}
			_, _ = fmt.Fprintf(w, `{"status":"shutting_down","service":%q,"pid":%d}`, daemonmeta.ServiceName, os.Getpid())
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	if err := runfile.Write(cfg.runFile, runfile.Info{PID: os.Getpid(), Port: serverPort(t, srv.URL), StartedAt: time.Unix(100, 0).UTC()}); err != nil {
		t.Fatal(err)
	}

	out, _, err := executeCLI(t, Deps{
		ProcessAlive: func(pid int) bool { return pid == os.Getpid() },
	}, "stop", "--json")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-shutdownCalled:
	default:
		t.Fatal("stop did not call daemon shutdown endpoint")
	}
	if !strings.Contains(out, `"state": "stopped"`) {
		t.Fatalf("stop did not report stopped:\n%s", out)
	}
}

func TestStatusKeepsLiveProbeFailureUnhealthy(t *testing.T) {
	cfg := setConfigEnv(t)
	if err := runfile.Write(cfg.runFile, runfile.Info{PID: 4242, Port: closedPort(t), StartedAt: time.Unix(100, 0).UTC()}); err != nil {
		t.Fatal(err)
	}

	out, _, err := executeCLI(t, Deps{
		ProcessAlive: func(pid int) bool { return pid == 4242 },
	}, "status", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"state": "unhealthy"`) {
		t.Fatalf("status should keep live probe failures unhealthy:\n%s", out)
	}
	info, err := runfile.Read(cfg.runFile)
	if err != nil {
		t.Fatal(err)
	}
	if info == nil {
		t.Fatal("live probe failure should not remove run-file")
	}
}

func TestStopRefusesUnverifiedLivePID(t *testing.T) {
	cfg := setConfigEnv(t)
	if err := runfile.Write(cfg.runFile, runfile.Info{PID: 4242, Port: closedPort(t), StartedAt: time.Unix(100, 0).UTC()}); err != nil {
		t.Fatal(err)
	}

	_, _, err := executeCLI(t, Deps{
		ProcessAlive: func(pid int) bool { return pid == 4242 },
	}, "stop", "--json")
	if err == nil {
		t.Fatal("stop should fail when daemon ownership cannot be verified")
	}
	info, err := runfile.Read(cfg.runFile)
	if err != nil {
		t.Fatal(err)
	}
	if info == nil {
		t.Fatal("unverified live PID should remain tracked")
	}
}

func TestStartDoesNotSpawnWhenLiveProbeFails(t *testing.T) {
	cfg := setConfigEnv(t)
	if err := runfile.Write(cfg.runFile, runfile.Info{PID: 4242, Port: closedPort(t), StartedAt: time.Unix(100, 0).UTC()}); err != nil {
		t.Fatal(err)
	}

	var started bool
	_, _, err := executeCLI(t, Deps{
		ProcessAlive: func(pid int) bool { return pid == 4242 },
		StartProcess: func(processStartConfig) error {
			started = true
			return nil
		},
	}, "start", "--timeout", "1ns", "--json")
	if err == nil {
		t.Fatal("start should fail instead of spawning over a live unverified PID")
	}
	if started {
		t.Fatal("start spawned while run-file PID was still alive")
	}
}

type testConfig struct {
	runFile string
	dataDir string
}

func setConfigEnv(t *testing.T) testConfig {
	t.Helper()
	dir := t.TempDir()
	cfg := testConfig{
		runFile: filepath.Join(dir, "running.json"),
		dataDir: filepath.Join(dir, "data"),
	}
	t.Setenv("AO_RUN_FILE", cfg.runFile)
	t.Setenv("AO_DATA_DIR", cfg.dataDir)
	t.Setenv("AO_PORT", "3001")
	t.Setenv("AO_REQUEST_TIMEOUT", "")
	t.Setenv("AO_SHUTDOWN_TIMEOUT", "")
	return cfg
}

func executeCLI(t *testing.T, deps Deps, args ...string) (string, string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	deps.Out = &out
	deps.Err = &errOut
	if deps.Sleep == nil {
		deps.Sleep = func(time.Duration) {}
	}
	cmd := NewRootCommand(deps)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

func serverPort(t *testing.T, raw string) int {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	_, portRaw, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portRaw)
	if err != nil {
		t.Fatal(err)
	}
	return port
}

func closedPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	_, portRaw, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portRaw)
	if err != nil {
		t.Fatal(err)
	}
	return port
}
