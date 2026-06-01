package codex

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
	codexHooksDirName  = ".codex"
	codexHooksFileName = "hooks.json"
	codexHooksTemplate = ".codex/hooks.json"

	codexConfigFileName        = "config.toml"
	codexHooksFeatureLine      = "hooks = true"
	codexLegacyHookFeatureLine = "codex_hooks = true"
)

//go:embed .codex/hooks.json
var codexHookTemplateFS embed.FS

type codexHookFile struct {
	Hooks map[string][]codexMatcherGroup `json:"hooks"`
}

type codexMatcherGroup struct {
	Matcher *string          `json:"matcher"`
	Hooks   []codexHookEntry `json:"hooks"`
}

type codexHookEntry struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"`
}

// GetAgentHooks installs Better-AO's Codex hooks into the worktree-local
// .codex/hooks.json file. Existing hook entries are preserved and duplicate
// Better-AO commands are not appended.
func (p *Plugin) GetAgentHooks(ctx context.Context, cfg agent.WorkspaceHookConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.WorkspacePath) == "" {
		return errors.New("codex.GetAgentHooks: WorkspacePath is required")
	}

	hooksPath := filepath.Join(cfg.WorkspacePath, codexHooksDirName, codexHooksFileName)
	topLevel := map[string]json.RawMessage{}
	rawHooks := map[string]json.RawMessage{}

	if existingData, err := os.ReadFile(hooksPath); err == nil {
		if len(strings.TrimSpace(string(existingData))) > 0 {
			if err := json.Unmarshal(existingData, &topLevel); err != nil {
				return fmt.Errorf("codex.GetAgentHooks: parse %s: %w", hooksPath, err)
			}
			if hooksRaw, ok := topLevel["hooks"]; ok {
				if err := json.Unmarshal(hooksRaw, &rawHooks); err != nil {
					return fmt.Errorf("codex.GetAgentHooks: parse hooks in %s: %w", hooksPath, err)
				}
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("codex.GetAgentHooks: read %s: %w", hooksPath, err)
	}

	templateHooks, err := codexEmbeddedHookGroups()
	if err != nil {
		return err
	}
	for event, templateGroups := range templateHooks {
		var existingGroups []codexMatcherGroup
		if err := parseCodexHookType(rawHooks, event, &existingGroups); err != nil {
			return err
		}
		for _, group := range templateGroups {
			for _, hook := range group.Hooks {
				if !codexHookCommandExists(existingGroups, hook.Command) {
					existingGroups = addCodexHook(existingGroups, hook)
				}
			}
		}
		if err := marshalCodexHookType(rawHooks, event, existingGroups); err != nil {
			return err
		}
	}

	hooksJSON, err := json.Marshal(rawHooks)
	if err != nil {
		return fmt.Errorf("codex.GetAgentHooks: encode hooks: %w", err)
	}
	topLevel["hooks"] = hooksJSON

	if err := os.MkdirAll(filepath.Dir(hooksPath), 0o750); err != nil {
		return fmt.Errorf("codex.GetAgentHooks: create hook dir: %w", err)
	}
	data, err := json.MarshalIndent(topLevel, "", "  ")
	if err != nil {
		return fmt.Errorf("codex.GetAgentHooks: encode %s: %w", hooksPath, err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(hooksPath, data, 0o600); err != nil {
		return fmt.Errorf("codex.GetAgentHooks: write %s: %w", hooksPath, err)
	}

	if err := ensureCodexHooksFeatureEnabled(cfg.WorkspacePath); err != nil {
		return fmt.Errorf("codex.GetAgentHooks: enable hooks feature: %w", err)
	}
	return nil
}

func codexEmbeddedHookGroups() (map[string][]codexMatcherGroup, error) {
	data, err := codexHookTemplateFS.ReadFile(codexHooksTemplate)
	if err != nil {
		return nil, fmt.Errorf("codex.GetAgentHooks: read embedded %s: %w", codexHooksTemplate, err)
	}
	var file codexHookFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("codex.GetAgentHooks: parse embedded %s: %w", codexHooksTemplate, err)
	}
	if file.Hooks == nil {
		return map[string][]codexMatcherGroup{}, nil
	}
	return file.Hooks, nil
}

func parseCodexHookType(rawHooks map[string]json.RawMessage, event string, target *[]codexMatcherGroup) error {
	data, ok := rawHooks[event]
	if !ok {
		return nil
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("codex.GetAgentHooks: parse %s hooks: %w", event, err)
	}
	return nil
}

func marshalCodexHookType(rawHooks map[string]json.RawMessage, event string, groups []codexMatcherGroup) error {
	if len(groups) == 0 {
		delete(rawHooks, event)
		return nil
	}
	data, err := json.Marshal(groups)
	if err != nil {
		return fmt.Errorf("codex.GetAgentHooks: encode %s hooks: %w", event, err)
	}
	rawHooks[event] = data
	return nil
}

func codexHookCommandExists(groups []codexMatcherGroup, command string) bool {
	for _, group := range groups {
		for _, hook := range group.Hooks {
			if hook.Command == command {
				return true
			}
		}
	}
	return false
}

func addCodexHook(groups []codexMatcherGroup, hook codexHookEntry) []codexMatcherGroup {
	for i, group := range groups {
		if group.Matcher == nil {
			groups[i].Hooks = append(groups[i].Hooks, hook)
			return groups
		}
	}
	return append(groups, codexMatcherGroup{
		Matcher: nil,
		Hooks:   []codexHookEntry{hook},
	})
}

func ensureCodexHooksFeatureEnabled(workspacePath string) error {
	configPath := filepath.Join(workspacePath, codexHooksDirName, codexConfigFileName)
	data, err := os.ReadFile(configPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read config.toml: %w", err)
	}

	content := string(data)
	hasNew := containsCodexFeatureLine(content, codexHooksFeatureLine)
	hasLegacy := containsCodexFeatureLine(content, codexLegacyHookFeatureLine)
	switch {
	case hasNew && hasLegacy:
		content = stripCodexLegacyHookFeatureLine(content)
	case hasNew:
		return nil
	case hasLegacy:
		content = strings.Replace(content, codexLegacyHookFeatureLine, codexHooksFeatureLine, 1)
	case strings.Contains(content, "[features]"):
		content = strings.Replace(content, "[features]", "[features]\n"+codexHooksFeatureLine, 1)
	default:
		if len(content) > 0 && !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		content += "\n[features]\n" + codexHooksFeatureLine + "\n"
	}

	if err := os.MkdirAll(filepath.Dir(configPath), 0o750); err != nil {
		return fmt.Errorf("create .codex directory: %w", err)
	}
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		return fmt.Errorf("write config.toml: %w", err)
	}
	return nil
}

func containsCodexFeatureLine(content string, line string) bool {
	for raw := range strings.SplitSeq(content, "\n") {
		if strings.TrimSpace(raw) == line {
			return true
		}
	}
	return false
}

func stripCodexLegacyHookFeatureLine(content string) string {
	idx := strings.Index(content, codexLegacyHookFeatureLine)
	if idx < 0 {
		return content
	}
	end := idx + len(codexLegacyHookFeatureLine)
	if end < len(content) && content[end] == '\n' {
		end++
	}
	return content[:idx] + content[end:]
}
