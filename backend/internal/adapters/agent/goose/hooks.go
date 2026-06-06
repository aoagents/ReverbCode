package goose

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
	// goosePluginDirName is the AO plugin directory under a workspace's
	// .agents/plugins/. Goose auto-discovers any plugin dir containing a
	// hooks/hooks.json at startup; unlike Codex there is no separate feature
	// flag to toggle, so installing the file is sufficient.
	gooseHooksRootDirName = ".agents"
	goosePluginsDirName   = "plugins"
	goosePluginName       = "ao"
	gooseHooksSubDirName  = "hooks"
	gooseHooksFileName    = "hooks.json"

	// gooseHookCommandPrefix identifies the hook commands AO owns, so install
	// skips duplicates and uninstall recognizes AO entries by prefix without an
	// embedded template to diff against.
	gooseHookCommandPrefix = "ao hooks goose "
	gooseHookTimeout       = 30
)

// gooseHookFile is the on-disk shape of .agents/plugins/ao/hooks/hooks.json. It
// is used by tests to decode the written file.
type gooseHookFile struct {
	Hooks map[string][]gooseMatcherGroup `json:"hooks"`
}

type gooseMatcherGroup struct {
	Matcher *string          `json:"matcher,omitempty"`
	Hooks   []gooseHookEntry `json:"hooks"`
}

type gooseHookEntry struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"`
}

// gooseHookSpec describes one hook AO installs, defined in code rather than read
// from an embedded hooks file.
type gooseHookSpec struct {
	Event   string
	Command string
}

// gooseManagedHooks is the source of truth for the hooks AO installs. Goose
// groups every hook under the nil matcher. Goose has no permission/approval
// lifecycle event yet, so AO installs only the session/prompt/stop signals.
var gooseManagedHooks = []gooseHookSpec{
	{Event: "SessionStart", Command: gooseHookCommandPrefix + "session-start"},
	{Event: "UserPromptSubmit", Command: gooseHookCommandPrefix + "user-prompt-submit"},
	{Event: "Stop", Command: gooseHookCommandPrefix + "stop"},
}

// GetAgentHooks installs AO's Goose hooks into the worktree-local
// .agents/plugins/ao/hooks/hooks.json file. Existing hook entries are preserved
// and duplicate AO commands are not appended.
func (p *Plugin) GetAgentHooks(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.WorkspacePath) == "" {
		return errors.New("goose.GetAgentHooks: WorkspacePath is required")
	}

	hooksPath := gooseHooksPath(cfg.WorkspacePath)
	topLevel, rawHooks, err := readGooseHooks(hooksPath)
	if err != nil {
		return fmt.Errorf("goose.GetAgentHooks: %w", err)
	}

	for event, specs := range groupGooseHooksByEvent() {
		var existingGroups []gooseMatcherGroup
		if err := parseGooseHookType(rawHooks, event, &existingGroups); err != nil {
			return fmt.Errorf("goose.GetAgentHooks: %w", err)
		}
		for _, spec := range specs {
			if !gooseHookCommandExists(existingGroups, spec.Command) {
				entry := gooseHookEntry{Type: "command", Command: spec.Command, Timeout: gooseHookTimeout}
				existingGroups = addGooseHook(existingGroups, entry)
			}
		}
		if err := marshalGooseHookType(rawHooks, event, existingGroups); err != nil {
			return fmt.Errorf("goose.GetAgentHooks: %w", err)
		}
	}

	if err := writeGooseHooks(hooksPath, topLevel, rawHooks); err != nil {
		return fmt.Errorf("goose.GetAgentHooks: %w", err)
	}
	return nil
}

// UninstallHooks removes AO's Goose hooks from the workspace-local
// .agents/plugins/ao/hooks/hooks.json file, leaving user-defined hooks
// untouched. A missing file is a no-op.
func (p *Plugin) UninstallHooks(ctx context.Context, workspacePath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(workspacePath) == "" {
		return errors.New("goose.UninstallHooks: workspacePath is required")
	}

	hooksPath := gooseHooksPath(workspacePath)
	if _, err := os.Stat(hooksPath); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	topLevel, rawHooks, err := readGooseHooks(hooksPath)
	if err != nil {
		return fmt.Errorf("goose.UninstallHooks: %w", err)
	}

	for _, event := range gooseManagedEvents() {
		var groups []gooseMatcherGroup
		if err := parseGooseHookType(rawHooks, event, &groups); err != nil {
			return fmt.Errorf("goose.UninstallHooks: %w", err)
		}
		groups = removeGooseManagedHooks(groups)
		if err := marshalGooseHookType(rawHooks, event, groups); err != nil {
			return fmt.Errorf("goose.UninstallHooks: %w", err)
		}
	}

	if err := writeGooseHooks(hooksPath, topLevel, rawHooks); err != nil {
		return fmt.Errorf("goose.UninstallHooks: %w", err)
	}
	return nil
}

// AreHooksInstalled reports whether any AO Goose hook is present in the
// workspace-local hooks file. A missing file means none are installed.
func (p *Plugin) AreHooksInstalled(ctx context.Context, workspacePath string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if strings.TrimSpace(workspacePath) == "" {
		return false, errors.New("goose.AreHooksInstalled: workspacePath is required")
	}

	hooksPath := gooseHooksPath(workspacePath)
	if _, err := os.Stat(hooksPath); errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	_, rawHooks, err := readGooseHooks(hooksPath)
	if err != nil {
		return false, fmt.Errorf("goose.AreHooksInstalled: %w", err)
	}

	for _, event := range gooseManagedEvents() {
		var groups []gooseMatcherGroup
		if err := parseGooseHookType(rawHooks, event, &groups); err != nil {
			return false, fmt.Errorf("goose.AreHooksInstalled: %w", err)
		}
		for _, group := range groups {
			for _, hook := range group.Hooks {
				if isGooseManagedHook(hook.Command) {
					return true, nil
				}
			}
		}
	}
	return false, nil
}

func gooseHooksPath(workspacePath string) string {
	return filepath.Join(workspacePath, gooseHooksRootDirName, goosePluginsDirName, goosePluginName, gooseHooksSubDirName, gooseHooksFileName)
}

// readGooseHooks loads the hooks file into a top-level raw map plus the decoded
// "hooks" sub-map, preserving keys AO doesn't manage. A missing or empty file
// yields empty maps.
func readGooseHooks(hooksPath string) (topLevel, rawHooks map[string]json.RawMessage, err error) {
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

// writeGooseHooks folds rawHooks back into topLevel and writes the file. An
// empty hooks map drops the "hooks" key entirely.
func writeGooseHooks(hooksPath string, topLevel, rawHooks map[string]json.RawMessage) error {
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
// write can't leave a truncated/empty file that Goose then fails to parse.
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

// groupGooseHooksByEvent groups the managed hook specs by their Goose event so
// each event's array is rewritten once.
func groupGooseHooksByEvent() map[string][]gooseHookSpec {
	byEvent := map[string][]gooseHookSpec{}
	for _, spec := range gooseManagedHooks {
		byEvent[spec.Event] = append(byEvent[spec.Event], spec)
	}
	return byEvent
}

// gooseManagedEvents returns the distinct Goose events AO manages, in the order
// they first appear in gooseManagedHooks.
func gooseManagedEvents() []string {
	seen := map[string]bool{}
	events := make([]string, 0, len(gooseManagedHooks))
	for _, spec := range gooseManagedHooks {
		if !seen[spec.Event] {
			seen[spec.Event] = true
			events = append(events, spec.Event)
		}
	}
	return events
}

func isGooseManagedHook(command string) bool {
	return strings.HasPrefix(command, gooseHookCommandPrefix)
}

// removeGooseManagedHooks strips AO hook entries from every group, dropping any
// group left without hooks.
func removeGooseManagedHooks(groups []gooseMatcherGroup) []gooseMatcherGroup {
	result := make([]gooseMatcherGroup, 0, len(groups))
	for _, group := range groups {
		kept := make([]gooseHookEntry, 0, len(group.Hooks))
		for _, hook := range group.Hooks {
			if !isGooseManagedHook(hook.Command) {
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

func parseGooseHookType(rawHooks map[string]json.RawMessage, event string, target *[]gooseMatcherGroup) error {
	data, ok := rawHooks[event]
	if !ok {
		return nil
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("parse %s hooks: %w", event, err)
	}
	return nil
}

func marshalGooseHookType(rawHooks map[string]json.RawMessage, event string, groups []gooseMatcherGroup) error {
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

func gooseHookCommandExists(groups []gooseMatcherGroup, command string) bool {
	for _, group := range groups {
		for _, hook := range group.Hooks {
			if hook.Command == command {
				return true
			}
		}
	}
	return false
}

func addGooseHook(groups []gooseMatcherGroup, hook gooseHookEntry) []gooseMatcherGroup {
	for i, group := range groups {
		if group.Matcher == nil {
			groups[i].Hooks = append(groups[i].Hooks, hook)
			return groups
		}
	}
	return append(groups, gooseMatcherGroup{
		Matcher: nil,
		Hooks:   []gooseHookEntry{hook},
	})
}
