package qwen

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
	qwenSettingsDirName  = ".qwen"
	qwenSettingsFileName = "settings.json"

	// qwenHookCommandPrefix identifies the hook commands AO owns. Every managed
	// command starts with it, so install can skip duplicates and uninstall can
	// recognize AO entries by prefix without an embedded template to diff
	// against.
	qwenHookCommandPrefix = "ao hooks qwen "

	// qwenHookTimeout is in milliseconds: Qwen Code (a gemini-cli fork) measures
	// hook timeouts in ms, unlike Claude/Codex which use seconds.
	qwenHookTimeout = 30000
)

// qwenHookFile is the on-disk shape of the "hooks" sub-object of
// .qwen/settings.json. It is used by tests to decode the written file.
type qwenHookFile struct {
	Hooks map[string][]qwenMatcherGroup `json:"hooks"`
}

type qwenMatcherGroup struct {
	// Matcher is a pointer so it round-trips exactly: SessionStart targets the
	// payload "source" field with a "startup" matcher; UserPromptSubmit/Stop/
	// PermissionRequest omit it (Qwen ignores the matcher for those events).
	// omitempty drops a nil matcher on write.
	Matcher *string         `json:"matcher,omitempty"`
	Hooks   []qwenHookEntry `json:"hooks"`
}

type qwenHookEntry struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"`
}

// qwenHookSpec describes one hook AO installs, defined in code rather than read
// from an embedded settings file.
type qwenHookSpec struct {
	Event   string
	Matcher *string
	Command string
}

// qwenStartupMatcher is referenced by pointer so SessionStart serializes with
// its "startup" source matcher.
var qwenStartupMatcher = "startup"

// qwenManagedHooks is the source of truth for the hooks AO installs:
// SessionStart (under the "startup" source matcher), UserPromptSubmit,
// PermissionRequest, and Stop. They report normalized session metadata and
// activity-state signals back into AO's store (see DeriveActivityState). The
// AO sub-command names are FIXED and must match the cases in
// DeriveActivityState.
var qwenManagedHooks = []qwenHookSpec{
	{Event: "SessionStart", Matcher: &qwenStartupMatcher, Command: qwenHookCommandPrefix + "session-start"},
	{Event: "UserPromptSubmit", Command: qwenHookCommandPrefix + "user-prompt-submit"},
	{Event: "PermissionRequest", Command: qwenHookCommandPrefix + "permission-request"},
	{Event: "Stop", Command: qwenHookCommandPrefix + "stop"},
}

// GetAgentHooks installs AO's Qwen Code hooks into the worktree-local
// .qwen/settings.json file (the project-level settings). The hooks
// (SessionStart, UserPromptSubmit, PermissionRequest, Stop) report normalized
// session metadata and activity signals back into AO's store. Existing hooks
// and unrelated settings are preserved, and duplicate AO commands are not
// appended, so the install is idempotent.
func (p *Plugin) GetAgentHooks(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.WorkspacePath) == "" {
		return errors.New("qwen.GetAgentHooks: WorkspacePath is required")
	}

	settingsPath := qwenSettingsPath(cfg.WorkspacePath)
	topLevel, rawHooks, err := readQwenSettings(settingsPath)
	if err != nil {
		return fmt.Errorf("qwen.GetAgentHooks: %w", err)
	}

	for event, specs := range groupQwenHooksByEvent() {
		var existingGroups []qwenMatcherGroup
		if err := parseQwenHookType(rawHooks, event, &existingGroups); err != nil {
			return fmt.Errorf("qwen.GetAgentHooks: %w", err)
		}
		for _, spec := range specs {
			if !qwenHookCommandExists(existingGroups, spec.Command) {
				entry := qwenHookEntry{Type: "command", Command: spec.Command, Timeout: qwenHookTimeout}
				existingGroups = addQwenHook(existingGroups, entry, spec.Matcher)
			}
		}
		if err := marshalQwenHookType(rawHooks, event, existingGroups); err != nil {
			return fmt.Errorf("qwen.GetAgentHooks: %w", err)
		}
	}

	if err := writeQwenSettings(settingsPath, topLevel, rawHooks); err != nil {
		return fmt.Errorf("qwen.GetAgentHooks: %w", err)
	}
	return nil
}

// UninstallHooks removes AO's Qwen Code hooks from the workspace-local
// .qwen/settings.json file, leaving user-defined hooks and unrelated settings
// untouched. A missing settings file is a no-op.
func (p *Plugin) UninstallHooks(ctx context.Context, workspacePath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(workspacePath) == "" {
		return errors.New("qwen.UninstallHooks: workspacePath is required")
	}

	settingsPath := qwenSettingsPath(workspacePath)
	if _, err := os.Stat(settingsPath); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	topLevel, rawHooks, err := readQwenSettings(settingsPath)
	if err != nil {
		return fmt.Errorf("qwen.UninstallHooks: %w", err)
	}

	for _, event := range qwenManagedEvents() {
		var groups []qwenMatcherGroup
		if err := parseQwenHookType(rawHooks, event, &groups); err != nil {
			return fmt.Errorf("qwen.UninstallHooks: %w", err)
		}
		groups = removeQwenManagedHooks(groups)
		if err := marshalQwenHookType(rawHooks, event, groups); err != nil {
			return fmt.Errorf("qwen.UninstallHooks: %w", err)
		}
	}

	if err := writeQwenSettings(settingsPath, topLevel, rawHooks); err != nil {
		return fmt.Errorf("qwen.UninstallHooks: %w", err)
	}
	return nil
}

// AreHooksInstalled reports whether any AO Qwen Code hook is present in the
// workspace-local settings file. A missing file means none are installed.
func (p *Plugin) AreHooksInstalled(ctx context.Context, workspacePath string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if strings.TrimSpace(workspacePath) == "" {
		return false, errors.New("qwen.AreHooksInstalled: workspacePath is required")
	}

	settingsPath := qwenSettingsPath(workspacePath)
	if _, err := os.Stat(settingsPath); errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	_, rawHooks, err := readQwenSettings(settingsPath)
	if err != nil {
		return false, fmt.Errorf("qwen.AreHooksInstalled: %w", err)
	}

	for _, event := range qwenManagedEvents() {
		var groups []qwenMatcherGroup
		if err := parseQwenHookType(rawHooks, event, &groups); err != nil {
			return false, fmt.Errorf("qwen.AreHooksInstalled: %w", err)
		}
		for _, group := range groups {
			for _, hook := range group.Hooks {
				if isQwenManagedHook(hook.Command) {
					return true, nil
				}
			}
		}
	}
	return false, nil
}

func qwenSettingsPath(workspacePath string) string {
	return filepath.Join(workspacePath, qwenSettingsDirName, qwenSettingsFileName)
}

// readQwenSettings loads the settings file into a top-level raw map plus the
// decoded "hooks" sub-map, preserving every key AO doesn't manage. A missing or
// empty file yields empty maps.
func readQwenSettings(settingsPath string) (topLevel, rawHooks map[string]json.RawMessage, err error) {
	topLevel = map[string]json.RawMessage{}
	rawHooks = map[string]json.RawMessage{}

	data, err := os.ReadFile(settingsPath) //nolint:gosec // path built from caller-owned workspace dir
	if errors.Is(err, os.ErrNotExist) {
		return topLevel, rawHooks, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("read %s: %w", settingsPath, err)
	}
	if strings.TrimSpace(string(data)) == "" {
		return topLevel, rawHooks, nil
	}
	if err := json.Unmarshal(data, &topLevel); err != nil {
		return nil, nil, fmt.Errorf("parse %s: %w", settingsPath, err)
	}
	if hooksRaw, ok := topLevel["hooks"]; ok {
		if err := json.Unmarshal(hooksRaw, &rawHooks); err != nil {
			return nil, nil, fmt.Errorf("parse hooks in %s: %w", settingsPath, err)
		}
	}
	return topLevel, rawHooks, nil
}

// writeQwenSettings folds rawHooks back into topLevel and writes the file. An
// empty hooks map drops the "hooks" key entirely.
func writeQwenSettings(settingsPath string, topLevel, rawHooks map[string]json.RawMessage) error {
	if len(rawHooks) == 0 {
		delete(topLevel, "hooks")
	} else {
		hooksJSON, err := json.Marshal(rawHooks)
		if err != nil {
			return fmt.Errorf("encode hooks: %w", err)
		}
		topLevel["hooks"] = hooksJSON
	}

	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o750); err != nil {
		return fmt.Errorf("create settings dir: %w", err)
	}
	data, err := json.MarshalIndent(topLevel, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", settingsPath, err)
	}
	data = append(data, '\n')
	if err := atomicWriteFile(settingsPath, data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", settingsPath, err)
	}
	return nil
}

// atomicWriteFile writes data to path via a temp file in the same directory
// followed by a rename, so a crash or signal mid-write can't leave a truncated
// or empty file that Qwen Code then fails to parse (silently disabling hooks).
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".ao-tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op once renamed
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// groupQwenHooksByEvent groups the managed hook specs by their Qwen event so
// each event's settings array is rewritten once.
func groupQwenHooksByEvent() map[string][]qwenHookSpec {
	byEvent := map[string][]qwenHookSpec{}
	for _, spec := range qwenManagedHooks {
		byEvent[spec.Event] = append(byEvent[spec.Event], spec)
	}
	return byEvent
}

// qwenManagedEvents returns the distinct Qwen events AO manages, in the order
// they first appear in qwenManagedHooks.
func qwenManagedEvents() []string {
	seen := map[string]bool{}
	events := make([]string, 0, len(qwenManagedHooks))
	for _, spec := range qwenManagedHooks {
		if !seen[spec.Event] {
			seen[spec.Event] = true
			events = append(events, spec.Event)
		}
	}
	return events
}

func isQwenManagedHook(command string) bool {
	return strings.HasPrefix(command, qwenHookCommandPrefix)
}

// removeQwenManagedHooks strips AO hook entries from every group, dropping any
// group left without hooks so the event array doesn't accumulate empty matcher
// objects.
func removeQwenManagedHooks(groups []qwenMatcherGroup) []qwenMatcherGroup {
	result := make([]qwenMatcherGroup, 0, len(groups))
	for _, group := range groups {
		kept := make([]qwenHookEntry, 0, len(group.Hooks))
		for _, hook := range group.Hooks {
			if !isQwenManagedHook(hook.Command) {
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

func parseQwenHookType(rawHooks map[string]json.RawMessage, event string, target *[]qwenMatcherGroup) error {
	data, ok := rawHooks[event]
	if !ok {
		return nil
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("parse %s hooks: %w", event, err)
	}
	return nil
}

func marshalQwenHookType(rawHooks map[string]json.RawMessage, event string, groups []qwenMatcherGroup) error {
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

func qwenHookCommandExists(groups []qwenMatcherGroup, command string) bool {
	for _, group := range groups {
		for _, hook := range group.Hooks {
			if hook.Command == command {
				return true
			}
		}
	}
	return false
}

// addQwenHook appends hook to an existing group with the same matcher (so a
// SessionStart hook lands under its "startup" matcher), creating that group if
// none matches.
func addQwenHook(groups []qwenMatcherGroup, hook qwenHookEntry, matcher *string) []qwenMatcherGroup {
	for i, group := range groups {
		if matchersEqual(group.Matcher, matcher) {
			groups[i].Hooks = append(groups[i].Hooks, hook)
			return groups
		}
	}
	return append(groups, qwenMatcherGroup{Matcher: matcher, Hooks: []qwenHookEntry{hook}})
}

func matchersEqual(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
