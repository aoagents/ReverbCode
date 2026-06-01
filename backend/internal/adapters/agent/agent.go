package agent

import (
	"context"
)

// Agent defines the behavior every CLI coding agent adapter must provide.
type Agent interface {
	// GetConfigSpec describes the agent-specific config keys Better-AO can
	// expose to users in ~/.better-ao/config.yaml.
	GetConfigSpec(ctx context.Context) (ConfigSpec, error)

	// GetLaunchCommand builds the command Better-AO should run to start this agent.
	GetLaunchCommand(ctx context.Context, cfg LaunchConfig) (cmd []string, err error)

	// GetPromptDeliveryStrategy tells Better-AO whether the prompt is included in
	// the launch command or must be sent after the agent process starts.
	GetPromptDeliveryStrategy(ctx context.Context, cfg LaunchConfig) (PromptDeliveryStrategy, error)

	// GetAgentHooks installs or merges Better-AO hooks into the agent's
	// native workspace-local hook config. It must preserve user-defined hooks.
	GetAgentHooks(ctx context.Context, cfg WorkspaceHookConfig) error

	// GetRestoreCommand builds a command that continues an existing native agent
	// session. ok=false means no existing native session can be continued.
	GetRestoreCommand(ctx context.Context, cfg RestoreConfig) (cmd []string, ok bool, err error)

	// SessionInfo reads agent-owned session metadata such as native session id,
	// display title, or summary. ok=false means no info is available.
	SessionInfo(ctx context.Context, session SessionRef) (info SessionInfo, ok bool, err error)
}

// Config contains values loaded from the selected agent's config section.
// Agent adapters own validation for their custom keys.
type Config map[string]any

// ConfigSpec describes the agent-specific config keys AO can expose to users.
type ConfigSpec struct {
	Fields []ConfigField
}

// ConfigField describes one user-facing agent config key.
type ConfigField struct {
	Key         string
	Type        ConfigFieldType
	Description string
	Required    bool
	Default     any
	Enum        []string
}

// ConfigFieldType is the primitive value kind Better-AO expects for a field.
type ConfigFieldType string

const (
	ConfigFieldString     ConfigFieldType = "string"
	ConfigFieldBool       ConfigFieldType = "bool"
	ConfigFieldNumber     ConfigFieldType = "number"
	ConfigFieldStringList ConfigFieldType = "string_list"
	ConfigFieldEnum       ConfigFieldType = "enum"
)

// LaunchConfig carries inputs needed to build a new agent launch command.
type LaunchConfig struct {
	Config           Config
	IssueID          string
	Permissions      PermissionMode
	Prompt           string
	SessionID        string
	SystemPrompt     string
	SystemPromptFile string
	WorkspacePath    string
}

// WorkspaceHookConfig carries inputs needed to install workspace-local agent hooks.
type WorkspaceHookConfig struct {
	Config        Config
	DataDir       string
	SessionID     string
	WorkspacePath string
}

// RestoreConfig carries inputs needed to continue an existing native agent session.
type RestoreConfig struct {
	Config      Config
	Permissions PermissionMode
	Session     SessionRef
}

// SessionRef identifies a Better-AO session whose agent-owned metadata may be read.
type SessionRef struct {
	ID            string
	Metadata      map[string]string
	WorkspacePath string
}

// SessionInfo contains agent-owned session metadata.
type SessionInfo struct {
	AgentSessionID string
	Metadata       map[string]string
	Title          string
	Summary        string
}

// PermissionMode controls how much review an agent requires before acting.
type PermissionMode string

const (
	// "default" is special: adapters emit no flag for it so the agent resolves
	// its starting mode from the user's own config (e.g. Claude's TUI reading
	// ~/.claude/settings.json defaultMode).
	PermissionModeDefault           PermissionMode = "default"
	PermissionModeAcceptEdits       PermissionMode = "accept-edits"
	PermissionModeAuto              PermissionMode = "auto"
	PermissionModeBypassPermissions PermissionMode = "bypass-permissions"
)

// PromptDeliveryStrategy describes how Better-AO should deliver the initial prompt.
type PromptDeliveryStrategy string

const (
	PromptDeliveryInCommand  PromptDeliveryStrategy = "in_command"
	PromptDeliveryAfterStart PromptDeliveryStrategy = "after_start"
)
