package project

import "github.com/aoagents/agent-orchestrator/backend/internal/domain"

// Summary is the row shape returned by GET /api/v1/projects.
type Summary struct {
	ID            domain.ProjectID `json:"id"`
	Name          string           `json:"name"`
	SessionPrefix string           `json:"sessionPrefix"`
	ResolveError  string           `json:"resolveError,omitempty"`
}

// Project is the full read-model returned by GET /api/v1/projects/{id}.
type Project struct {
	ID            domain.ProjectID `json:"id"`
	Name          string           `json:"name"`
	Path          string           `json:"path"`
	Repo          string           `json:"repo"`
	DefaultBranch string           `json:"defaultBranch"`
	Agent         string           `json:"agent,omitempty"`
	Tracker       *TrackerConfig   `json:"tracker,omitempty"`
	SCM           *SCMConfig       `json:"scm,omitempty"`
}

// Degraded is returned in place of Project when project config failed to load.
type Degraded struct {
	ID           domain.ProjectID `json:"id"`
	Name         string           `json:"name"`
	Path         string           `json:"path"`
	ResolveError string           `json:"resolveError"`
}

// TrackerConfig mirrors tracker behaviour config exposed by the projects API.
type TrackerConfig struct {
	Plugin  string `json:"plugin,omitempty"`
	Package string `json:"package,omitempty"`
	Path    string `json:"path,omitempty"`
}

// SCMConfig mirrors SCM behaviour config exposed by the projects API.
type SCMConfig struct {
	Plugin  string            `json:"plugin,omitempty"`
	Package string            `json:"package,omitempty"`
	Path    string            `json:"path,omitempty"`
	Webhook *SCMWebhookConfig `json:"webhook,omitempty"`
}

// SCMWebhookConfig describes SCM webhook settings.
type SCMWebhookConfig struct {
	Enabled         *bool  `json:"enabled,omitempty"`
	Path            string `json:"path,omitempty"`
	SecretEnvVar    string `json:"secretEnvVar,omitempty"`
	SignatureHeader string `json:"signatureHeader,omitempty"`
	EventHeader     string `json:"eventHeader,omitempty"`
	DeliveryHeader  string `json:"deliveryHeader,omitempty"`
	MaxBodyBytes    int    `json:"maxBodyBytes,omitempty"`
}
