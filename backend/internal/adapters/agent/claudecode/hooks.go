package claudecode

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent"
)

const (
	claudeSettingsDirName  = ".claude"
	claudeSettingsFileName = "settings.local.json"
	claudeHooksTemplate    = ".claude/settings.local.json"
)

//go:embed .claude/settings.local.json
var claudeHookTemplateFS embed.FS

type claudeHookFile struct {
	Hooks map[string][]claudeMatcherGroup `json:"hooks"`
}

type claudeMatcherGroup struct {
	// Matcher is a pointer so it round-trips exactly: SessionStart requires a
	// real matcher ("startup"); UserPromptSubmit/Stop omit it (Claude ignores
	// matcher for those events). omitempty drops a nil matcher on write.
	Matcher *string           `json:"matcher,omitempty"`
	Hooks   []claudeHookEntry `json:"hooks"`
}

type claudeHookEntry struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"`
}

// GetAgentHooks installs Better-AO's Claude Code hooks into the worktree-local
// .claude/settings.local.json file (the per-session local settings, not the
// shared .claude/settings.json). The hooks (SessionStart, UserPromptSubmit,
// Stop) report normalized session metadata back into Better-AO's store. Existing
// hooks and unrelated settings are preserved, and duplicate Better-AO commands
// are not appended, so the install is idempotent.
func (p *Plugin) GetAgentHooks(ctx context.Context, cfg agent.WorkspaceHookConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.WorkspacePath) == "" {
		return errors.New("claude-code.GetAgentHooks: WorkspacePath is required")
	}

	settingsPath := filepath.Join(cfg.WorkspacePath, claudeSettingsDirName, claudeSettingsFileName)
	// Preserve every top-level setting (permissions, model, …) and every hook
	// event we don't touch by keeping them as raw JSON.
	topLevel := map[string]json.RawMessage{}
	rawHooks := map[string]json.RawMessage{}

	if existingData, err := os.ReadFile(settingsPath); err == nil {
		if len(strings.TrimSpace(string(existingData))) > 0 {
			if err := json.Unmarshal(existingData, &topLevel); err != nil {
				return fmt.Errorf("claude-code.GetAgentHooks: parse %s: %w", settingsPath, err)
			}
			if hooksRaw, ok := topLevel["hooks"]; ok {
				if err := json.Unmarshal(hooksRaw, &rawHooks); err != nil {
					return fmt.Errorf("claude-code.GetAgentHooks: parse hooks in %s: %w", settingsPath, err)
				}
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("claude-code.GetAgentHooks: read %s: %w", settingsPath, err)
	}

	templateHooks, err := claudeEmbeddedHookGroups()
	if err != nil {
		return err
	}
	for event, templateGroups := range templateHooks {
		var existingGroups []claudeMatcherGroup
		if err := parseClaudeHookType(rawHooks, event, &existingGroups); err != nil {
			return err
		}
		for _, group := range templateGroups {
			for _, hook := range group.Hooks {
				if !claudeHookCommandExists(existingGroups, hook.Command) {
					existingGroups = addClaudeHook(existingGroups, hook, group.Matcher)
				}
			}
		}
		if err := marshalClaudeHookType(rawHooks, event, existingGroups); err != nil {
			return err
		}
	}

	hooksJSON, err := json.Marshal(rawHooks)
	if err != nil {
		return fmt.Errorf("claude-code.GetAgentHooks: encode hooks: %w", err)
	}
	topLevel["hooks"] = hooksJSON

	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o750); err != nil {
		return fmt.Errorf("claude-code.GetAgentHooks: create settings dir: %w", err)
	}
	data, err := json.MarshalIndent(topLevel, "", "  ")
	if err != nil {
		return fmt.Errorf("claude-code.GetAgentHooks: encode %s: %w", settingsPath, err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(settingsPath, data, 0o600); err != nil {
		return fmt.Errorf("claude-code.GetAgentHooks: write %s: %w", settingsPath, err)
	}
	return nil
}

func claudeEmbeddedHookGroups() (map[string][]claudeMatcherGroup, error) {
	data, err := claudeHookTemplateFS.ReadFile(claudeHooksTemplate)
	if err != nil {
		return nil, fmt.Errorf("claude-code.GetAgentHooks: read embedded %s: %w", claudeHooksTemplate, err)
	}
	var file claudeHookFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("claude-code.GetAgentHooks: parse embedded %s: %w", claudeHooksTemplate, err)
	}
	if file.Hooks == nil {
		return map[string][]claudeMatcherGroup{}, nil
	}
	return file.Hooks, nil
}

func parseClaudeHookType(rawHooks map[string]json.RawMessage, event string, target *[]claudeMatcherGroup) error {
	data, ok := rawHooks[event]
	if !ok {
		return nil
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("claude-code.GetAgentHooks: parse %s hooks: %w", event, err)
	}
	return nil
}

func marshalClaudeHookType(rawHooks map[string]json.RawMessage, event string, groups []claudeMatcherGroup) error {
	if len(groups) == 0 {
		delete(rawHooks, event)
		return nil
	}
	data, err := json.Marshal(groups)
	if err != nil {
		return fmt.Errorf("claude-code.GetAgentHooks: encode %s hooks: %w", event, err)
	}
	rawHooks[event] = data
	return nil
}

func claudeHookCommandExists(groups []claudeMatcherGroup, command string) bool {
	for _, group := range groups {
		for _, hook := range group.Hooks {
			if hook.Command == command {
				return true
			}
		}
	}
	return false
}

// addClaudeHook appends hook to an existing group with the same matcher (so a
// SessionStart hook lands under its "startup" matcher), creating that group if
// none matches.
func addClaudeHook(groups []claudeMatcherGroup, hook claudeHookEntry, matcher *string) []claudeMatcherGroup {
	for i, group := range groups {
		if matchersEqual(group.Matcher, matcher) {
			groups[i].Hooks = append(groups[i].Hooks, hook)
			return groups
		}
	}
	return append(groups, claudeMatcherGroup{Matcher: matcher, Hooks: []claudeHookEntry{hook}})
}

func matchersEqual(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
