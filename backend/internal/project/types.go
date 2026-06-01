package project

import "github.com/aoagents/agent-orchestrator/backend/internal/domain"

// Project entities and behaviour-config shapes live here, not in domain/,
// because only project identity (domain.ProjectID) is shared vocabulary; the
// rest is owned by the projects surface.

// Summary is the row shape returned by GET /api/v1/projects. ResolveError is
// set only for degraded projects so the list can flag them rather than drop
// them.
type Summary struct {
	ID            domain.ProjectID `json:"id"`
	Name          string           `json:"name"`
	SessionPrefix string           `json:"sessionPrefix"`
	ResolveError  string           `json:"resolveError,omitempty" description:"Present iff the project is degraded."`
}

// Project is the full read-model returned by GET /api/v1/projects/{id} when the
// project resolves cleanly: registry identity joined with behaviour config.
type Project struct {
	ID            domain.ProjectID           `json:"id"`
	Name          string                     `json:"name"`
	Path          string                     `json:"path"`
	Repo          string                     `json:"repo" description:"\"owner/name\" or empty string when unset"`
	DefaultBranch string                     `json:"defaultBranch" default:"main"`
	Agent         AgentConfig                `json:"agent,omitempty" description:"Agent config blob (open object)"`
	Runtime       RuntimeConfig              `json:"runtime,omitempty" description:"Runtime (terminal multiplexer) config blob (open object)"`
	Tracker       *TrackerConfig             `json:"tracker,omitempty"`
	SCM           *SCMConfig                 `json:"scm,omitempty"`
	Reactions     map[string]*ReactionConfig `json:"reactions,omitempty"`
}

// Degraded replaces Project when the project's config failed to load.
// ResolveError drives the frontend's recovery UI. Repair is deferred (see the
// Manager doc), so a degraded project is read-only for now.
type Degraded struct {
	ID           domain.ProjectID `json:"id"`
	Name         string           `json:"name"`
	Path         string           `json:"path"`
	ResolveError string           `json:"resolveError"`
}

// Behaviour-config shapes ported from the TS Zod schemas. Only the fields the
// API exposes are modelled; passthrough of unknown keys lands with the handler
// impl, not this interface-only PR.

// AgentConfig and RuntimeConfig are open config objects (the legacy local
// config models agent/runtime as provider-defined blocks, not bare strings).
// Concrete fields are ported with the handler impl; a nil map omits the field.
type (
	AgentConfig   = map[string]any
	RuntimeConfig = map[string]any
)

// TrackerConfig mirrors TrackerConfigSchema.
type TrackerConfig struct {
	Plugin  string `json:"plugin,omitempty"`
	Package string `json:"package,omitempty"`
	Path    string `json:"path,omitempty"`
}

// SCMConfig mirrors SCMConfigSchema; Webhook nests its own optional block.
type SCMConfig struct {
	Plugin  string            `json:"plugin,omitempty"`
	Package string            `json:"package,omitempty"`
	Path    string            `json:"path,omitempty"`
	Webhook *SCMWebhookConfig `json:"webhook,omitempty"`
}

// SCMWebhookConfig — pointer Enabled distinguishes unset from explicit false.
type SCMWebhookConfig struct {
	Enabled         *bool  `json:"enabled,omitempty"`
	Path            string `json:"path,omitempty"`
	SecretEnvVar    string `json:"secretEnvVar,omitempty"`
	SignatureHeader string `json:"signatureHeader,omitempty"`
	EventHeader     string `json:"eventHeader,omitempty"`
	DeliveryHeader  string `json:"deliveryHeader,omitempty"`
	MaxBodyBytes    int    `json:"maxBodyBytes,omitempty"`
}

// ReactionConfig mirrors ReactionConfigSchema. EscalateAfter is either ms
// (number) or a duration string ("30m") in the TS schema, so it stays open as
// `any` until handler validation lands.
type ReactionConfig struct {
	Auto           *bool  `json:"auto,omitempty"`
	Action         string `json:"action,omitempty" enum:"send-to-agent,notify,auto-merge"`
	Message        string `json:"message,omitempty"`
	Priority       string `json:"priority,omitempty" enum:"urgent,action,warning,info"`
	Retries        *int   `json:"retries,omitempty"`
	EscalateAfter  any    `json:"escalateAfter,omitempty" description:"Either ms (number) or a duration string (\"30m\")"`
	Threshold      string `json:"threshold,omitempty"`
	IncludeSummary *bool  `json:"includeSummary,omitempty"`
}
