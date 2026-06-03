package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
)

type sessionRequestLog struct {
	mu       sync.Mutex
	requests []string
}

func (l *sessionRequestLog) append(r *http.Request) {
	l.mu.Lock()
	defer l.mu.Unlock()
	entry := r.Method + " " + r.URL.Path
	if r.URL.RawQuery != "" {
		entry += "?" + r.URL.RawQuery
	}
	l.requests = append(l.requests, entry)
}

func (l *sessionRequestLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.requests...)
}

func sessionCommandServer(t *testing.T) (*httptest.Server, *sessionRequestLog) {
	t.Helper()
	log := &sessionRequestLog{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.append(r)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/sessions":
			active := r.URL.Query().Get("active")
			switch active {
			case "false":
				_, _ = io.WriteString(w, `{"sessions":[`+
					sessionJSON("demo-old", "demo", "worker", "terminated", true)+`,`+
					sessionJSON("demo-orch", "demo", "orchestrator", "terminated", true)+`]}`)
			default:
				_, _ = io.WriteString(w, `{"sessions":[`+
					sessionJSON("demo-2", "demo", "orchestrator", "idle", false)+`,`+
					sessionJSON("demo-1", "demo", "worker", "working", false)+`]}`)
			}
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/sessions/demo-1":
			_, _ = io.WriteString(w, `{"session":`+sessionJSON("demo-1", "demo", "worker", "working", false)+`}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions/cleanup":
			_, _ = io.WriteString(w, `{"ok":true,"cleaned":["demo-old","demo-orch"]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions/demo-1/kill":
			_, _ = io.WriteString(w, `{"ok":true,"sessionId":"demo-1","freed":true}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions/demo-1/restore":
			_, _ = io.WriteString(w, `{"ok":true,"sessionId":"demo-1","session":`+sessionJSON("demo-1", "demo", "worker", "idle", false)+`}`)
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/sessions/demo-1":
			var req sessionRenameRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			_, _ = io.WriteString(w, `{"ok":true,"sessionId":"demo-1","displayName":`+jsonQuote(req.DisplayName)+`}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, log
}

func sessionJSON(id, project, kind, status string, terminated bool) string {
	b, _ := json.Marshal(map[string]any{
		"id":           id,
		"projectId":    project,
		"kind":         kind,
		"harness":      "codex",
		"displayName":  "Current Name",
		"activity":     map[string]any{"state": "idle", "lastActivityAt": "2026-06-02T12:00:00Z"},
		"isTerminated": terminated,
		"createdAt":    "2026-06-02T11:00:00Z",
		"updatedAt":    "2026-06-02T12:00:00Z",
		"status":       status,
	})
	return string(b)
}

func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestSessionList_ProjectFilterAndDefaultFiltering(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, log := sessionCommandServer(t)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "session", "ls", "--project", "demo")
	if err != nil {
		t.Fatalf("session ls failed: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, "demo:") || !strings.Contains(out, "demo-1") {
		t.Fatalf("output missing worker session:\n%s", out)
	}
	if strings.Contains(out, "demo-2") {
		t.Fatalf("orchestrator session should be hidden without --all:\n%s", out)
	}
	if !strings.Contains(out, "1 terminated session hidden") {
		t.Fatalf("hidden terminated hint missing:\n%s", out)
	}
	want := []string{
		"GET /api/v1/sessions?active=true&project=demo",
		"GET /api/v1/sessions?active=false&project=demo",
	}
	if got := log.all(); !reflect.DeepEqual(got, want) {
		t.Fatalf("requests = %#v, want %#v", got, want)
	}
}

func TestSessionList_JSONOutputDecodes(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := sessionCommandServer(t)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "session", "ls", "--project", "demo", "--json")
	if err != nil {
		t.Fatalf("session ls --json failed: %v\nstderr=%s", err, errOut)
	}
	var got sessionListOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("session ls --json output is not decodable: %v\noutput=%s", err, out)
	}
	if got.Meta.HiddenTerminatedCount != 1 {
		t.Fatalf("hiddenTerminatedCount = %d, want 1", got.Meta.HiddenTerminatedCount)
	}
	if len(got.Data) != 1 {
		t.Fatalf("len(data) = %d, want 1; data=%#v", len(got.Data), got.Data)
	}
	if got.Data[0].ID != "demo-1" || got.Data[0].ProjectID != "demo" || got.Data[0].Role != "worker" {
		t.Fatalf("unexpected JSON entry: %#v", got.Data[0])
	}
}

func TestSessionGet_SuccessWithProjectScope(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, log := sessionCommandServer(t)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "session", "get", "demo-1", "-p", "demo")
	if err != nil {
		t.Fatalf("session get failed: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, "id: demo-1") || !strings.Contains(out, "project: demo") {
		t.Fatalf("unexpected get output:\n%s", out)
	}
	want := []string{"GET /api/v1/sessions/demo-1"}
	if got := log.all(); !reflect.DeepEqual(got, want) {
		t.Fatalf("requests = %#v, want %#v", got, want)
	}
}

func TestSessionGet_JSONOutputDecodes(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := sessionCommandServer(t)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "session", "get", "demo-1", "--project", "demo", "--json")
	if err != nil {
		t.Fatalf("session get --json failed: %v\nstderr=%s", err, errOut)
	}
	var got sessionResponse
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("session get --json output is not decodable: %v\noutput=%s", err, out)
	}
	if got.Session.ID != "demo-1" || got.Session.ProjectID != "demo" || got.Session.Status != "working" {
		t.Fatalf("unexpected session JSON: %#v", got.Session)
	}
}

func TestSessionKill_SuccessWithProjectScope(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, log := sessionCommandServer(t)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "session", "kill", "demo-1", "--project", "demo")
	if err != nil {
		t.Fatalf("session kill failed: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, "session demo-1 killed") {
		t.Fatalf("unexpected kill output:\n%s", out)
	}
	want := []string{"GET /api/v1/sessions/demo-1", "POST /api/v1/sessions/demo-1/kill"}
	if got := log.all(); !reflect.DeepEqual(got, want) {
		t.Fatalf("requests = %#v, want %#v", got, want)
	}
}

func TestSessionRestore_SuccessWithProjectScope(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, log := sessionCommandServer(t)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "session", "restore", "demo-1", "-p", "demo")
	if err != nil {
		t.Fatalf("session restore failed: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, "session demo-1 restored") || !strings.Contains(out, "project: demo") {
		t.Fatalf("unexpected restore output:\n%s", out)
	}
	want := []string{"GET /api/v1/sessions/demo-1", "POST /api/v1/sessions/demo-1/restore"}
	if got := log.all(); !reflect.DeepEqual(got, want) {
		t.Fatalf("requests = %#v, want %#v", got, want)
	}
}

func TestSessionCleanup_YesSkipsPrompt(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, log := sessionCommandServer(t)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		In:           strings.NewReader("no\n"),
		ProcessAlive: func(int) bool { return true },
	}, "session", "cleanup", "--project", "demo", "--yes")
	if err != nil {
		t.Fatalf("session cleanup failed: %v\nstderr=%s", err, errOut)
	}
	if strings.Contains(out, "Type yes to confirm") {
		t.Fatalf("--yes should skip confirmation prompt:\n%s", out)
	}
	for _, want := range []string{"Checking for completed sessions", "Would clean demo-old", "Would clean demo-orch", "Cleaned: demo-old", "Cleaned: demo-orch", "Cleanup complete. 2 sessions cleaned."} {
		if !strings.Contains(out, want) {
			t.Fatalf("cleanup output missing %q:\n%s", want, out)
		}
	}
	want := []string{
		"GET /api/v1/sessions?active=false&project=demo",
		"POST /api/v1/sessions/cleanup?project=demo",
	}
	if got := log.all(); !reflect.DeepEqual(got, want) {
		t.Fatalf("requests = %#v, want %#v", got, want)
	}
}

func TestSessionCleanup_PromptFailsWithoutInput(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, log := sessionCommandServer(t)
	writeRunFileFor(t, cfg, srv)

	out, _, err := executeCLI(t, Deps{
		In:           strings.NewReader(""),
		ProcessAlive: func(int) bool { return true },
	}, "session", "cleanup", "--project", "demo")
	if err == nil {
		t.Fatal("expected cleanup prompt without input to fail")
	}
	if got := ExitCode(err); got != 1 {
		t.Fatalf("exit code = %d, want 1", got)
	}
	if !strings.Contains(out, "Type yes to confirm") {
		t.Fatalf("output missing confirmation prompt:\n%s", out)
	}
	want := []string{"GET /api/v1/sessions?active=false&project=demo"}
	if got := log.all(); !reflect.DeepEqual(got, want) {
		t.Fatalf("requests = %#v, want %#v", got, want)
	}
}

func TestSessionRename_SuccessWithProjectScope(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, log := sessionCommandServer(t)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "session", "rename", "demo-1", "New Name", "-p", "demo")
	if err != nil {
		t.Fatalf("session rename failed: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, `session demo-1 renamed to "New Name"`) {
		t.Fatalf("unexpected rename output:\n%s", out)
	}
	want := []string{"GET /api/v1/sessions/demo-1", "PATCH /api/v1/sessions/demo-1"}
	if got := log.all(); !reflect.DeepEqual(got, want) {
		t.Fatalf("requests = %#v, want %#v", got, want)
	}
}

func TestSessionCommands_MissingIDIsUsageError(t *testing.T) {
	setConfigEnv(t)
	for _, sub := range []string{"get", "kill", "restore"} {
		t.Run(sub, func(t *testing.T) {
			_, _, err := executeCLI(t, Deps{}, "session", sub)
			if err == nil {
				t.Fatal("expected missing id to fail")
			}
			if got := ExitCode(err); got != 2 {
				t.Fatalf("exit code = %d, want 2 (err=%v)", got, err)
			}
		})
	}
}

func TestSessionRename_MissingNameIsUsageError(t *testing.T) {
	setConfigEnv(t)

	_, _, err := executeCLI(t, Deps{}, "session", "rename", "demo-1")
	if err == nil {
		t.Fatal("expected missing name to fail")
	}
	if got := ExitCode(err); got != 2 {
		t.Fatalf("exit code = %d, want 2 (err=%v)", got, err)
	}
}

func TestSessionGet_ProjectMismatchDoesNotPassScope(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := sessionCommandServer(t)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "session", "get", "demo-1", "--project", "other")
	if err == nil {
		t.Fatal("expected project mismatch to fail")
	}
	if got := ExitCode(err); got != 2 {
		t.Fatalf("exit code = %d, want 2", got)
	}
	if !strings.Contains(err.Error(), "not in project other") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSessionRename_ProjectMismatchDoesNotPatch(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, log := sessionCommandServer(t)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "session", "rename", "demo-1", "New Name", "--project", "other")
	if err == nil {
		t.Fatal("expected project mismatch to fail")
	}
	if got := ExitCode(err); got != 2 {
		t.Fatalf("exit code = %d, want 2", got)
	}
	if !strings.Contains(err.Error(), "not in project other") {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"GET /api/v1/sessions/demo-1"}
	if got := log.all(); !reflect.DeepEqual(got, want) {
		t.Fatalf("requests = %#v, want %#v", got, want)
	}
}
