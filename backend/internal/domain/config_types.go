package domain

// Typed configuration shapes ported from the TS Zod schemas in the old
// orchestrator (packages/core/src/config.ts + global-config.ts). They live in
// the domain package because both the project read-model (Project) and the
// future project-config service surface them on the wire.
//
// Zod .passthrough() preserves unknown keys; the Go equivalent will be a
// custom UnmarshalJSON that fills Extra. The route-shell PR (#20) only declares
// the shapes for documentation in planned bodies — the passthrough Marshal/
// Unmarshal lands when a real handler first reads or writes config.

// AgentPermission constrains the agent-permissions enum. Empty string means
// "not set" — distinct from the TS default of "permissionless" so the API can
// tell user-set values apart from defaults.
type AgentPermission string

const (
	AgentPermissionPermissionless AgentPermission = "permissionless"
	AgentPermissionDefault        AgentPermission = "default"
	AgentPermissionAutoEdit       AgentPermission = "auto-edit"
	AgentPermissionSuggest        AgentPermission = "suggest"
	AgentPermissionSkip           AgentPermission = "skip"
)

// TrackerConfig mirrors TrackerConfigSchema. .passthrough() preserves arbitrary
// plugin-specific keys; Extra is reserved for that round-trip.
type TrackerConfig struct {
	Plugin  string         `json:"plugin,omitempty"`
	Package string         `json:"package,omitempty"`
	Path    string         `json:"path,omitempty"`
	Extra   map[string]any `json:"-"`
}

// SCMConfig mirrors SCMConfigSchema. Webhook nests its own optional block.
type SCMConfig struct {
	Plugin  string            `json:"plugin,omitempty"`
	Package string            `json:"package,omitempty"`
	Path    string            `json:"path,omitempty"`
	Webhook *SCMWebhookConfig `json:"webhook,omitempty"`
	Extra   map[string]any    `json:"-"`
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

// AgentConfig mirrors AgentSpecificConfigSchema. .passthrough() preserves
// agent-plugin-specific keys.
type AgentConfig struct {
	Permissions       AgentPermission `json:"permissions,omitempty"`
	Model             string          `json:"model,omitempty"`
	OrchestratorModel string          `json:"orchestratorModel,omitempty"`
	OpenCodeSessionID string          `json:"opencodeSessionId,omitempty"`
	Extra             map[string]any  `json:"-"`
}

// RoleAgentConfig mirrors RoleAgentConfigSchema for orchestrator/worker roles.
type RoleAgentConfig struct {
	Agent       string       `json:"agent,omitempty"`
	AgentConfig *AgentConfig `json:"agentConfig,omitempty"`
}

// ReactionConfig mirrors ReactionConfigSchema. EscalateAfter accepts both
// numeric milliseconds and a duration string (e.g. "30m") in the TS schema, so
// the Go type stays open as `any` until handler validation lands.
type ReactionConfig struct {
	Auto           *bool  `json:"auto,omitempty"`
	Action         string `json:"action,omitempty"`
	Message        string `json:"message,omitempty"`
	Priority       string `json:"priority,omitempty"`
	Retries        *int   `json:"retries,omitempty"`
	EscalateAfter  any    `json:"escalateAfter,omitempty"`
	Threshold      string `json:"threshold,omitempty"`
	IncludeSummary *bool  `json:"includeSummary,omitempty"`
}

// LocalProjectConfig mirrors LocalProjectConfigSchema — the flat, behavior-only
// on-disk file at <project>/agent-orchestrator.yaml. Identity fields
// (projectId, path, repo, defaultBranch, sessionPrefix) deliberately live in
// the global registry, not here.
type LocalProjectConfig struct {
	Repo          string                     `json:"repo,omitempty"`
	DefaultBranch string                     `json:"defaultBranch,omitempty"`
	Runtime       string                     `json:"runtime,omitempty"`
	Agent         string                     `json:"agent,omitempty"`
	Workspace     string                     `json:"workspace,omitempty"`
	Tracker       *TrackerConfig             `json:"tracker,omitempty"`
	SCM           *SCMConfig                 `json:"scm,omitempty"`
	Symlinks      []string                   `json:"symlinks,omitempty"`
	PostCreate    []string                   `json:"postCreate,omitempty"`
	AgentConfig   *AgentConfig               `json:"agentConfig,omitempty"`
	Orchestrator  *RoleAgentConfig           `json:"orchestrator,omitempty"`
	Worker        *RoleAgentConfig           `json:"worker,omitempty"`
	Reactions     map[string]*ReactionConfig `json:"reactions,omitempty"`
	AgentRules    string                     `json:"agentRules,omitempty"`
	Extra         map[string]any             `json:"-"`
}
