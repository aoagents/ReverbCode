package domain

// Project domain types for the HTTP API. ProjectID already exists in
// session.go; this file adds the list/read read-models the projects route
// surface returns.

// ProjectSummary is the row shape returned by GET /api/v1/projects. It mirrors
// the TS ProjectInfo (packages/web/src/lib/project-name.ts) so the existing
// dashboard list view can read the Go daemon's response unchanged.
//
// ResolveError is populated only for degraded projects — entries whose config
// failed to load but whose registry entry still exists. The list view shows
// them with a warning instead of dropping them silently.
type ProjectSummary struct {
	ID            ProjectID `json:"id"`
	Name          string    `json:"name"`
	SessionPrefix string    `json:"sessionPrefix"`
	ResolveError  string    `json:"resolveError,omitempty"`
}

// Project is the full read-model returned by GET /api/v1/projects/{id} when
// the project resolves cleanly. It joins the global-registry identity fields
// (id, name, path, repo, defaultBranch) with the local on-disk behaviour
// config (agent, runtime, tracker, scm, reactions).
type Project struct {
	ID            ProjectID                  `json:"id"`
	Name          string                     `json:"name"`
	Path          string                     `json:"path"`
	Repo          string                     `json:"repo"`
	DefaultBranch string                     `json:"defaultBranch"`
	Agent         string                     `json:"agent,omitempty"`
	Runtime       string                     `json:"runtime,omitempty"`
	Tracker       *TrackerConfig             `json:"tracker,omitempty"`
	SCM           *SCMConfig                 `json:"scm,omitempty"`
	Reactions     map[string]*ReactionConfig `json:"reactions,omitempty"`
}

// DegradedProject is returned in place of Project when the project's config
// failed to load. The frontend uses ResolveError to render a recovery UI; the
// /projects/{id}/repair endpoint accepts the project id to fix a recoverable
// subset of degraded states (e.g. legacy wrapped-config format).
type DegradedProject struct {
	ID           ProjectID `json:"id"`
	Name         string    `json:"name"`
	Path         string    `json:"path"`
	ResolveError string    `json:"resolveError"`
}
