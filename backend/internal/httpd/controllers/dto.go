package controllers

import (
	"encoding/json"

	"github.com/aoagents/agent-orchestrator/backend/internal/project"
)

// HTTP response envelopes for the projects surface — the SINGLE definition of
// each wire shape. The handlers encode these (httpx.WriteJSON), and
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

// ProjectResponse is the { project } body shared by POST /projects (201) and
// PATCH /projects/{id} (200).
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

func (p ProjectOrDegraded) MarshalJSON() ([]byte, error) {
	if p.Degraded != nil {
		return json.Marshal(p.Degraded)
	}
	return json.Marshal(p.Project)
}

// JSONSchemaOneOf is read by swaggest's reflector (apispec.Build) to emit the
// oneOf for this field; it is not used at runtime.
func (ProjectOrDegraded) JSONSchemaOneOf() []interface{} {
	return []interface{}{project.Project{}, project.Degraded{}}
}

// newGetProjectResponse maps the internal GetResult onto the wire envelope —
// the explicit project→httpd boundary the result type exists for.
func newGetProjectResponse(res project.GetResult) GetProjectResponse {
	return GetProjectResponse{
		Status:  res.Status,
		Project: ProjectOrDegraded{Project: res.Project, Degraded: res.Degraded},
	}
}
