package controllers_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/legacyimport"
	importsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/importer"
)

type fakeImportService struct {
	status    importsvc.Status
	statusErr error
	report    legacyimport.Report
	runErr    error
	runs      int
}

func (f *fakeImportService) Status(context.Context) (importsvc.Status, error) {
	return f.status, f.statusErr
}

func (f *fakeImportService) Run(context.Context) (legacyimport.Report, error) {
	f.runs++
	return f.report, f.runErr
}

func newImportTestServer(t *testing.T, svc *fakeImportService) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{Import: svc}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

func TestImportAPI_Status(t *testing.T) {
	svc := &fakeImportService{status: importsvc.Status{Available: true, LegacyRoot: "/home/u/.agent-orchestrator"}}
	srv := newImportTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/import", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, body)
	}
	var resp struct {
		Available  bool   `json:"available"`
		LegacyRoot string `json:"legacyRoot"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.Available || resp.LegacyRoot != "/home/u/.agent-orchestrator" {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestImportAPI_StatusError(t *testing.T) {
	svc := &fakeImportService{statusErr: errors.New("boom")}
	srv := newImportTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/import", "")
	if status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", status, body)
	}
}

func TestImportAPI_Run(t *testing.T) {
	svc := &fakeImportService{report: legacyimport.Report{ProjectsImported: 2, ProjectsSkipped: 1}}
	srv := newImportTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/import", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, body)
	}
	if svc.runs != 1 {
		t.Fatalf("runs = %d, want 1", svc.runs)
	}
	var resp struct {
		Report struct {
			ProjectsImported int `json:"projectsImported"`
			ProjectsSkipped  int `json:"projectsSkipped"`
		} `json:"report"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Report.ProjectsImported != 2 || resp.Report.ProjectsSkipped != 1 {
		t.Fatalf("report = %+v", resp.Report)
	}
}

func TestImportAPI_RunError(t *testing.T) {
	svc := &fakeImportService{runErr: errors.New("disk full")}
	srv := newImportTestServer(t, svc)

	_, status, _ := doRequest(t, srv, "POST", "/api/v1/import", "")
	if status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", status)
	}
}

func TestImportAPI_NotImplementedWhenNilService(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)

	_, status, _ := doRequest(t, srv, "GET", "/api/v1/import", "")
	if status != http.StatusNotImplemented {
		t.Fatalf("GET status = %d, want 501", status)
	}
	_, status, _ = doRequest(t, srv, "POST", "/api/v1/import", "")
	if status != http.StatusNotImplemented {
		t.Fatalf("POST status = %d, want 501", status)
	}
}
