package agy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	agyHooksDirName  = ".gemini"
	agyHooksFileName = "hooks.json"

	agyHookCommandPrefix = "ao hooks agy "
)

type agyHookFile struct {
	Hooks map[string][]agyMatcherGroup `json:"hooks"`
}

type agyMatcherGroup struct {
	Matcher *string        `json:"matcher,omitempty"`
	Hooks   []agyHookEntry `json:"hooks"`
}

type agyHookEntry struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

type agyHookSpec struct {
	Event   string
	Command string
}

var agyManagedHooks = []agyHookSpec{
	{Event: "SessionStart", Command: agyHookCommandPrefix + "session-start"},
	{Event: "SessionEnd", Command: agyHookCommandPrefix + "session-end"},
	{Event: "BeforeAgent", Command: agyHookCommandPrefix + "before-agent"},
	{Event: "AfterAgent", Command: agyHookCommandPrefix + "after-agent"},
	{Event: "AfterTool", Command: agyHookCommandPrefix + "after-tool"},
}

// GetAgentHooks installs AO's Agy hooks into the worktree-local
// .gemini/hooks.json file. Existing hook entries are preserved and duplicate
// AO commands are not appended.
func (p *Plugin) GetAgentHooks(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.WorkspacePath) == "" {
		return errors.New("agy.GetAgentHooks: WorkspacePath is required")
	}

	hooksPath := agyHooksPath(cfg.WorkspacePath)
	topLevel, rawHooks, err := readAgyHooks(hooksPath)
	if err != nil {
		return fmt.Errorf("agy.GetAgentHooks: %w", err)
	}

	for event, specs := range groupAgyHooksByEvent() {
		var existingGroups []agyMatcherGroup
		if err := parseAgyHookType(rawHooks, event, &existingGroups); err != nil {
			return fmt.Errorf("agy.GetAgentHooks: %w", err)
		}
		for _, spec := range specs {
			if !agyHookCommandExists(existingGroups, spec.Command) {
				entry := agyHookEntry{Type: "command", Command: spec.Command}
				existingGroups = addAgyHook(existingGroups, entry)
			}
		}
		if err := marshalAgyHookType(rawHooks, event, existingGroups); err != nil {
			return fmt.Errorf("agy.GetAgentHooks: %w", err)
		}
	}

	if err := writeAgyHooks(hooksPath, topLevel, rawHooks); err != nil {
		return fmt.Errorf("agy.GetAgentHooks: %w", err)
	}

	return nil
}

// UninstallHooks removes AO's Agy hooks from the workspace-local
// .gemini/hooks.json file, leaving user-defined hooks untouched. A missing file
// is a no-op.
func (p *Plugin) UninstallHooks(ctx context.Context, workspacePath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(workspacePath) == "" {
		return errors.New("agy.UninstallHooks: workspacePath is required")
	}

	hooksPath := agyHooksPath(workspacePath)
	if _, err := os.Stat(hooksPath); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	topLevel, rawHooks, err := readAgyHooks(hooksPath)
	if err != nil {
		return fmt.Errorf("agy.UninstallHooks: %w", err)
	}

	for _, event := range agyManagedEvents() {
		var groups []agyMatcherGroup
		if err := parseAgyHookType(rawHooks, event, &groups); err != nil {
			return fmt.Errorf("agy.UninstallHooks: %w", err)
		}
		groups = removeAgyManagedHooks(groups)
		if err := marshalAgyHookType(rawHooks, event, groups); err != nil {
			return fmt.Errorf("agy.UninstallHooks: %w", err)
		}
	}

	if err := writeAgyHooks(hooksPath, topLevel, rawHooks); err != nil {
		return fmt.Errorf("agy.UninstallHooks: %w", err)
	}
	return nil
}

// AreHooksInstalled reports whether any AO Agy hook is present in the
// workspace-local hooks file. A missing file means none are installed.
func (p *Plugin) AreHooksInstalled(ctx context.Context, workspacePath string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if strings.TrimSpace(workspacePath) == "" {
		return false, errors.New("agy.AreHooksInstalled: workspacePath is required")
	}

	hooksPath := agyHooksPath(workspacePath)
	if _, err := os.Stat(hooksPath); errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	_, rawHooks, err := readAgyHooks(hooksPath)
	if err != nil {
		return false, fmt.Errorf("agy.AreHooksInstalled: %w", err)
	}

	for _, event := range agyManagedEvents() {
		var groups []agyMatcherGroup
		if err := parseAgyHookType(rawHooks, event, &groups); err != nil {
			return false, fmt.Errorf("agy.AreHooksInstalled: %w", err)
		}
		for _, group := range groups {
			for _, hook := range group.Hooks {
				if isAgyManagedHook(hook.Command) {
					return true, nil
				}
			}
		}
	}
	return false, nil
}

func agyHooksPath(workspacePath string) string {
	return filepath.Join(workspacePath, agyHooksDirName, agyHooksFileName)
}

// readAgyHooks loads the hooks file into a top-level raw map plus the decoded
// "hooks" sub-map, preserving keys AO doesn't manage. A missing or empty
// file yields empty maps.
func readAgyHooks(hooksPath string) (topLevel, rawHooks map[string]json.RawMessage, err error) {
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

// writeAgyHooks folds rawHooks back into topLevel and writes the file. An
// empty hooks map drops the "hooks" key entirely.
func writeAgyHooks(hooksPath string, topLevel, rawHooks map[string]json.RawMessage) error {
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
	if err := hookutil.AtomicWriteFile(hooksPath, data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", hooksPath, err)
	}
	return nil
}

func groupAgyHooksByEvent() map[string][]agyHookSpec {
	byEvent := map[string][]agyHookSpec{}
	for _, spec := range agyManagedHooks {
		byEvent[spec.Event] = append(byEvent[spec.Event], spec)
	}
	return byEvent
}

func agyManagedEvents() []string {
	seen := map[string]bool{}
	events := make([]string, 0, len(agyManagedHooks))
	for _, spec := range agyManagedHooks {
		if !seen[spec.Event] {
			seen[spec.Event] = true
			events = append(events, spec.Event)
		}
	}
	return events
}

func isAgyManagedHook(command string) bool {
	return strings.HasPrefix(command, agyHookCommandPrefix)
}

func removeAgyManagedHooks(groups []agyMatcherGroup) []agyMatcherGroup {
	result := make([]agyMatcherGroup, 0, len(groups))
	for _, group := range groups {
		kept := make([]agyHookEntry, 0, len(group.Hooks))
		for _, hook := range group.Hooks {
			if !isAgyManagedHook(hook.Command) {
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

func parseAgyHookType(rawHooks map[string]json.RawMessage, event string, target *[]agyMatcherGroup) error {
	data, ok := rawHooks[event]
	if !ok {
		return nil
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("parse %s hooks: %w", event, err)
	}
	return nil
}

func marshalAgyHookType(rawHooks map[string]json.RawMessage, event string, groups []agyMatcherGroup) error {
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

func agyHookCommandExists(groups []agyMatcherGroup, command string) bool {
	for _, group := range groups {
		for _, hook := range group.Hooks {
			if hook.Command == command {
				return true
			}
		}
	}
	return false
}

func addAgyHook(groups []agyMatcherGroup, hook agyHookEntry) []agyMatcherGroup {
	for i, group := range groups {
		if group.Matcher == nil {
			groups[i].Hooks = append(groups[i].Hooks, hook)
			return groups
		}
	}
	return append(groups, agyMatcherGroup{Matcher: nil, Hooks: []agyHookEntry{hook}})
}
