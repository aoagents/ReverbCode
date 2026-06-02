package controllers

import (
	"encoding/json"
	"errors"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/project"
)

// HTTP response envelopes for the projects surface — the SINGLE definition of
// each wire shape. The handlers encode these (envelope.WriteJSON), and
// apispec.Build reflects these same types into openapi.yaml, so the served
// contract and the generated spec can't disagree. The request side needs no
// wrappers: handlers decode the body straight into the project commands
// (project.AddInput / project.UpdateConfigInput), which apispec also reflects.

// ProjectIDParam is the {id} path parameter shared by the /projects/{id}
// routes. Handlers read it via chi.URLParam (see projectID); it is declared here
// so every wire input/output shape has one home, and apispec.Build reflects it
// as the path parameter.
type ProjectIDParam struct {
	ID string `path:"id" description:"Project identifier (registry key)."`
}

// ListProjectsResponse is the body of GET /api/v1/projects.
type ListProjectsResponse struct {
	Projects []project.Summary `json:"projects"`
}

// ProjectResponse is the { project } body shared by POST /projects (201),
// PATCH /projects/{id} (200), and POST /projects/{id}/repair (200).
type ProjectResponse struct {
	Project project.Project `json:"project"`
}

// GetProjectResponse is the { status, project } body of GET /projects/{id},
// where project is oneOf Project|Degraded discriminated by status.
type GetProjectResponse struct {
	Status  string            `json:"status" enum:"ok,degraded"`
	Project ProjectOrDegraded `json:"project"`
}

// ProjectOrDegraded is the discriminated `project` field: exactly one of
// Project/Degraded is set. It marshals as whichever is present (so the handler
// emits the right object) and exposes the oneOf variants to the spec reflector
// (so apispec.Build emits `oneOf: [Project, Degraded]`) — one type, both jobs.
type ProjectOrDegraded struct {
	Project  *project.Project
	Degraded *project.Degraded
}

// MarshalJSON emits whichever variant is set so the handler sends the right
// object. It errors when neither is set rather than emitting a contract-breaking
// `null` — though the handler validates that upstream and returns a 500 before
// writing, so this branch is an unreachable backstop (see newGetProjectResponse).
func (p ProjectOrDegraded) MarshalJSON() ([]byte, error) {
	switch {
	case p.Degraded != nil:
		return json.Marshal(p.Degraded)
	case p.Project != nil:
		return json.Marshal(p.Project)
	default:
		// Unreachable in practice: the handler validates the GetResult via
		// newGetProjectResponse and writes a 500 before committing the 200
		// status, so this never encodes. Kept as a last-resort backstop —
		// erroring is still better than emitting a contract-breaking `null`,
		// though by here the status is already sent, so the real guard is
		// upstream.
		return nil, errEmptyProjectOrDegraded
	}
}

// errEmptyProjectOrDegraded marks a GetResult that set neither variant — a
// Manager-contract violation. newGetProjectResponse returns it so the handler
// can map it to a 500 before any response bytes are written.
var errEmptyProjectOrDegraded = errors.New("controllers: GetResult has neither Project nor Degraded set")

// JSONSchemaOneOf is read by swaggest's reflector (apispec.Build) to emit the
// oneOf for this field; it is not used at runtime.
func (ProjectOrDegraded) JSONSchemaOneOf() []interface{} {
	return []interface{}{project.Project{}, project.Degraded{}}
}

// newGetProjectResponse maps the internal GetResult onto the wire envelope —
// the explicit project→httpd boundary the result type exists for. It errors
// when the result sets neither variant, so the handler can return a clean 500
// BEFORE writing the 200 status rather than flushing a truncated body.
func newGetProjectResponse(res project.GetResult) (GetProjectResponse, error) {
	if res.Project == nil && res.Degraded == nil {
		return GetProjectResponse{}, errEmptyProjectOrDegraded
	}
	return GetProjectResponse{
		Status:  res.Status,
		Project: ProjectOrDegraded{Project: res.Project, Degraded: res.Degraded},
	}, nil
}

// SessionIDParam is the {id} path parameter shared by session item and command
// routes.
type SessionIDParam struct {
	ID string `path:"id" description:"Session identifier."`
}

// ListSessionsQuery is the filter surface for GET /api/v1/sessions.
type ListSessionsQuery struct {
	Project          string `query:"project" description:"Optional project id filter."`
	Active           *bool  `query:"active" description:"Optional active/terminal filter."`
	OrchestratorOnly *bool  `query:"orchestratorOnly" description:"When true, return only orchestrator sessions."`
	Fresh            *bool  `query:"fresh" description:"Optional freshness filter for dashboard reads."`
}

// SpawnSessionRequest is the body of POST /api/v1/sessions.
type SpawnSessionRequest struct {
	ProjectID domain.ProjectID `json:"projectId" description:"Project that owns the session."`
	IssueID   domain.IssueID   `json:"issueId,omitempty" description:"Optional tracker issue id."`
	Prompt    string           `json:"prompt,omitempty" description:"Initial prompt passed to the agent."`
}

// ListSessionsResponse is the body of GET /api/v1/sessions.
type ListSessionsResponse struct {
	Sessions []domain.Session `json:"sessions"`
}

// SessionResponse is the { session } body shared by session read, spawn, and
// restore routes.
type SessionResponse struct {
	Session domain.Session `json:"session"`
}

// SendSessionRequest is the body of POST /api/v1/sessions/{id}/send.
type SendSessionRequest struct {
	Message string `json:"message" description:"Message to send to the session agent."`
}

// SendSessionResponse is the success body of POST /api/v1/sessions/{id}/send.
type SendSessionResponse struct {
	SessionID domain.SessionID `json:"sessionId"`
	Message   string           `json:"message"`
}
