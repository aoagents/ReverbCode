package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	codexHooksDirName  = ".codex"
	codexHooksFileName = "hooks.json"

	codexConfigFileName        = "config.toml"
	codexHooksFeatureLine      = "hooks = true"
	codexLegacyHookFeatureLine = "codex_hooks = true"

	// codexHookCommandPrefix identifies the hook commands AO owns, so
	// install skips duplicates and uninstall recognizes AO entries by
	// prefix without an embedded template to diff against.
	codexHookCommandPrefix = "ao hooks codex "
	codexHookTimeout       = 30
)

// codexHookFile is the on-disk shape of .codex/hooks.json. It is used by tests
// to decode the written file.
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

// codexHookSpec describes one hook AO installs, defined in code rather
// than read from an embedded hooks file.
type codexHookSpec struct {
	Event   string
	Command string
}

// codexManagedHooks is the source of truth for the hooks AO installs.
// Codex groups every hook under the nil matcher.
var codexManagedHooks = []codexHookSpec{
	{Event: "SessionStart", Command: codexHookCommandPrefix + "session-start"},
	{Event: "UserPromptSubmit", Command: codexHookCommandPrefix + "user-prompt-submit"},
	{Event: "Stop", Command: codexHookCommandPrefix + "stop"},
}

// GetAgentHooks installs AO's Codex hooks into the worktree-local
// .codex/hooks.json file. Existing hook entries are preserved and duplicate
// AO commands are not appended.
func (p *Plugin) GetAgentHooks(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.WorkspacePath) == "" {
		return errors.New("codex.GetAgentHooks: WorkspacePath is required")
	}

	hooksPath := codexHooksPath(cfg.WorkspacePath)
	topLevel, rawHooks, err := readCodexHooks(hooksPath)
	if err != nil {
		return fmt.Errorf("codex.GetAgentHooks: %w", err)
	}

	for event, specs := range groupCodexHooksByEvent() {
		var existingGroups []codexMatcherGroup
		if err := parseCodexHookType(rawHooks, event, &existingGroups); err != nil {
			return fmt.Errorf("codex.GetAgentHooks: %w", err)
		}
		for _, spec := range specs {
			if !codexHookCommandExists(existingGroups, spec.Command) {
				entry := codexHookEntry{Type: "command", Command: spec.Command, Timeout: codexHookTimeout}
				existingGroups = addCodexHook(existingGroups, entry)
			}
		}
		if err := marshalCodexHookType(rawHooks, event, existingGroups); err != nil {
			return fmt.Errorf("codex.GetAgentHooks: %w", err)
		}
	}

	if err := writeCodexHooks(hooksPath, topLevel, rawHooks); err != nil {
		return fmt.Errorf("codex.GetAgentHooks: %w", err)
	}

	if err := ensureCodexHooksFeatureEnabled(cfg.WorkspacePath); err != nil {
		return fmt.Errorf("codex.GetAgentHooks: enable hooks feature: %w", err)
	}
	return nil
}

// UninstallHooks removes AO's Codex hooks from the workspace-local
// .codex/hooks.json file, leaving user-defined hooks untouched. A missing file
// is a no-op. The .codex/config.toml `hooks = true` feature flag is left in
// place because it enables every Codex hook, not just AO's.
func (p *Plugin) UninstallHooks(ctx context.Context, workspacePath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(workspacePath) == "" {
		return errors.New("codex.UninstallHooks: workspacePath is required")
	}

	hooksPath := codexHooksPath(workspacePath)
	if _, err := os.Stat(hooksPath); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	topLevel, rawHooks, err := readCodexHooks(hooksPath)
	if err != nil {
		return fmt.Errorf("codex.UninstallHooks: %w", err)
	}

	for _, event := range codexManagedEvents() {
		var groups []codexMatcherGroup
		if err := parseCodexHookType(rawHooks, event, &groups); err != nil {
			return fmt.Errorf("codex.UninstallHooks: %w", err)
		}
		groups = removeCodexManagedHooks(groups)
		if err := marshalCodexHookType(rawHooks, event, groups); err != nil {
			return fmt.Errorf("codex.UninstallHooks: %w", err)
		}
	}

	if err := writeCodexHooks(hooksPath, topLevel, rawHooks); err != nil {
		return fmt.Errorf("codex.UninstallHooks: %w", err)
	}
	return nil
}

// AreHooksInstalled reports whether any AO Codex hook is present in the
// workspace-local hooks file. A missing file means none are installed.
func (p *Plugin) AreHooksInstalled(ctx context.Context, workspacePath string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if strings.TrimSpace(workspacePath) == "" {
		return false, errors.New("codex.AreHooksInstalled: workspacePath is required")
	}

	hooksPath := codexHooksPath(workspacePath)
	if _, err := os.Stat(hooksPath); errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	_, rawHooks, err := readCodexHooks(hooksPath)
	if err != nil {
		return false, fmt.Errorf("codex.AreHooksInstalled: %w", err)
	}

	for _, event := range codexManagedEvents() {
		var groups []codexMatcherGroup
		if err := parseCodexHookType(rawHooks, event, &groups); err != nil {
			return false, fmt.Errorf("codex.AreHooksInstalled: %w", err)
		}
		for _, group := range groups {
			for _, hook := range group.Hooks {
				if isCodexManagedHook(hook.Command) {
					return true, nil
				}
			}
		}
	}
	return false, nil
}

func codexHooksPath(workspacePath string) string {
	return filepath.Join(workspacePath, codexHooksDirName, codexHooksFileName)
}

// readCodexHooks loads the hooks file into a top-level raw map plus the decoded
// "hooks" sub-map, preserving keys AO doesn't manage. A missing or empty
// file yields empty maps.
func readCodexHooks(hooksPath string) (topLevel, rawHooks map[string]json.RawMessage, err error) {
	topLevel = map[string]json.RawMessage{}
	rawHooks = map[string]json.RawMessage{}

	data, err := os.ReadFile(hooksPath) //nolint:gosec // path built from caller-owned workspace dir
	if errors.Is(err, os.ErrNotExist) {
		return topLevel, rawHooks, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("read %s: %w", hooksPath, err)
	}
	if strings.TrimSpace(string(data)) == "" {
		return topLevel, rawHooks, nil
	}
	if err := json.Unmarshal(data, &topLevel); err != nil {
		return nil, nil, fmt.Errorf("parse %s: %w", hooksPath, err)
	}
	if hooksRaw, ok := topLevel["hooks"]; ok {
		if err := json.Unmarshal(hooksRaw, &rawHooks); err != nil {
			return nil, nil, fmt.Errorf("parse hooks in %s: %w", hooksPath, err)
		}
	}
	return topLevel, rawHooks, nil
}

// writeCodexHooks folds rawHooks back into topLevel and writes the file. An
// empty hooks map drops the "hooks" key entirely.
func writeCodexHooks(hooksPath string, topLevel, rawHooks map[string]json.RawMessage) error {
	if len(rawHooks) == 0 {
		delete(topLevel, "hooks")
	} else {
		hooksJSON, err := json.Marshal(rawHooks)
		if err != nil {
			return fmt.Errorf("encode hooks: %w", err)
		}
		topLevel["hooks"] = hooksJSON
	}

	if err := os.MkdirAll(filepath.Dir(hooksPath), 0o750); err != nil {
		return fmt.Errorf("create hook dir: %w", err)
	}
	data, err := json.MarshalIndent(topLevel, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", hooksPath, err)
	}
	data = append(data, '\n')
	if err := atomicWriteFile(hooksPath, data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", hooksPath, err)
	}
	return nil
}

// atomicWriteFile writes data to path via a temp file + rename, so a crash mid-
// write can't leave a truncated/empty file that Codex then fails to parse.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".ao-tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// groupCodexHooksByEvent groups the managed hook specs by their Codex event so
// each event's array is rewritten once.
func groupCodexHooksByEvent() map[string][]codexHookSpec {
	byEvent := map[string][]codexHookSpec{}
	for _, spec := range codexManagedHooks {
		byEvent[spec.Event] = append(byEvent[spec.Event], spec)
	}
	return byEvent
}

// codexManagedEvents returns the distinct Codex events AO manages, in the
// order they first appear in codexManagedHooks.
func codexManagedEvents() []string {
	seen := map[string]bool{}
	events := make([]string, 0, len(codexManagedHooks))
	for _, spec := range codexManagedHooks {
		if !seen[spec.Event] {
			seen[spec.Event] = true
			events = append(events, spec.Event)
		}
	}
	return events
}

func isCodexManagedHook(command string) bool {
	return strings.HasPrefix(command, codexHookCommandPrefix)
}

// removeCodexManagedHooks strips AO hook entries from every group,
// dropping any group left without hooks.
func removeCodexManagedHooks(groups []codexMatcherGroup) []codexMatcherGroup {
	result := make([]codexMatcherGroup, 0, len(groups))
	for _, group := range groups {
		kept := make([]codexHookEntry, 0, len(group.Hooks))
		for _, hook := range group.Hooks {
			if !isCodexManagedHook(hook.Command) {
				kept = append(kept, hook)
			}
		}
		if len(kept) > 0 {
			group.Hooks = kept
			result = append(result, group)
		}
	}
	return result
}

func parseCodexHookType(rawHooks map[string]json.RawMessage, event string, target *[]codexMatcherGroup) error {
	data, ok := rawHooks[event]
	if !ok {
		return nil
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("parse %s hooks: %w", event, err)
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
		return fmt.Errorf("encode %s hooks: %w", event, err)
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
	data, err := os.ReadFile(configPath) //nolint:gosec // path built from caller-owned workspace dir
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
		if content != "" && !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		content += "\n[features]\n" + codexHooksFeatureLine + "\n"
	}

	if err := os.MkdirAll(filepath.Dir(configPath), 0o750); err != nil {
		return fmt.Errorf("create .codex directory: %w", err)
	}
	if err := atomicWriteFile(configPath, []byte(content), 0o600); err != nil {
		return fmt.Errorf("write config.toml: %w", err)
	}
	return nil
}

func containsCodexFeatureLine(content, line string) bool {
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
