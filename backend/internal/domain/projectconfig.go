package domain

import (
	"fmt"
	"reflect"
)

// ProjectConfig is the typed per-project configuration — the SQLite twin of the
// legacy agent-orchestrator.yaml `projects.<id>` block. It is persisted as one
// JSON blob per project and resolved at spawn. Each field is typed and
// validated; there is no free-form map.
//
// Some fields are consumed at spawn today (DefaultBranch, Env, Symlinks,
// PostCreate, the rules, AgentConfig, and the role overrides). Others are
// persisted and validated but not yet consumed — Tracker, SCM, and
// OpencodeIssueSessionStrategy await the infrastructure that will read them, and
// SessionPrefix currently feeds only the display prefix (session-id generation
// is unchanged).
type ProjectConfig struct {
	// DefaultBranch is the base branch new session worktrees are created from.
	DefaultBranch string `json:"defaultBranch,omitempty"`
	// SessionPrefix overrides the displayed session-id prefix.
	SessionPrefix string `json:"sessionPrefix,omitempty"`

	// Env are extra environment variables forwarded into worker session
	// runtimes. AO-internal vars (AO_SESSION, AO_PROJECT_ID, …) always win.
	Env map[string]string `json:"env,omitempty"`
	// Symlinks are repo-relative paths symlinked into each session workspace.
	Symlinks []string `json:"symlinks,omitempty"`
	// PostCreate are shell commands run in the workspace after it is created.
	PostCreate []string `json:"postCreate,omitempty"`

	// AgentRules are inline rules appended to every agent prompt for the project.
	AgentRules string `json:"agentRules,omitempty"`
	// AgentRulesFile is a path (relative to the project) whose contents are
	// appended to every agent prompt.
	AgentRulesFile string `json:"agentRulesFile,omitempty"`
	// OrchestratorRules are inline rules appended to orchestrator prompts.
	OrchestratorRules string `json:"orchestratorRules,omitempty"`

	// AgentConfig is the default agent config for the project.
	AgentConfig AgentConfig `json:"agentConfig,omitempty"`
	// Worker and Orchestrator are role-specific harness/agent-config overrides.
	Worker       RoleOverride `json:"worker,omitempty"`
	Orchestrator RoleOverride `json:"orchestrator,omitempty"`

	// Tracker selects and configures the project's issue tracker (not yet consumed).
	Tracker TrackerConfig `json:"tracker,omitempty"`
	// SCM selects and configures the project's source-control integration (not yet consumed).
	SCM SCMConfig `json:"scm,omitempty"`
	// OpencodeIssueSessionStrategy controls OpenCode issue-session reuse (not yet consumed).
	OpencodeIssueSessionStrategy string `json:"opencodeIssueSessionStrategy,omitempty"`
}

// RoleOverride overrides the harness and/or agent config for a session role.
type RoleOverride struct {
	Harness     AgentHarness `json:"agent,omitempty"`
	AgentConfig AgentConfig  `json:"agentConfig,omitempty"`
}

// TrackerConfig selects and configures a project's issue tracker.
type TrackerConfig struct {
	Plugin string `json:"plugin,omitempty"`
	TeamID string `json:"teamId,omitempty"`
}

// SCMConfig selects and configures a project's source-control integration.
type SCMConfig struct {
	Plugin  string            `json:"plugin,omitempty"`
	Webhook *SCMWebhookConfig `json:"webhook,omitempty"`
}

// SCMWebhookConfig describes SCM webhook acceleration settings.
type SCMWebhookConfig struct {
	Path            string `json:"path,omitempty"`
	SecretEnvVar    string `json:"secretEnvVar,omitempty"`
	SignatureHeader string `json:"signatureHeader,omitempty"`
	EventHeader     string `json:"eventHeader,omitempty"`
	DeliveryHeader  string `json:"deliveryHeader,omitempty"`
	MaxBodyBytes    int    `json:"maxBodyBytes,omitempty"`
}

// The OpenCode issue-session strategies.
const (
	OpencodeSessionReuse  = "reuse"
	OpencodeSessionDelete = "delete"
	OpencodeSessionIgnore = "ignore"
)

// IsZero reports whether the config carries no settings, so storage can persist
// SQL NULL and resolution can skip an empty config.
func (c ProjectConfig) IsZero() bool {
	return reflect.DeepEqual(c, ProjectConfig{})
}

// Validate rejects values outside the typed vocabulary so a bad config is
// refused when it is set (CLI/API) rather than surfacing at spawn.
func (c ProjectConfig) Validate() error {
	if err := c.AgentConfig.Validate(); err != nil {
		return err
	}
	for role, ro := range map[string]RoleOverride{"worker": c.Worker, "orchestrator": c.Orchestrator} {
		if ro.Harness != "" && !ro.Harness.IsKnown() {
			return fmt.Errorf("%s.agent: unknown harness %q", role, ro.Harness)
		}
		if err := ro.AgentConfig.Validate(); err != nil {
			return fmt.Errorf("%s.%w", role, err)
		}
	}
	switch c.OpencodeIssueSessionStrategy {
	case "", OpencodeSessionReuse, OpencodeSessionDelete, OpencodeSessionIgnore:
	default:
		return fmt.Errorf("opencodeIssueSessionStrategy: want one of reuse, delete, ignore")
	}
	return nil
}
