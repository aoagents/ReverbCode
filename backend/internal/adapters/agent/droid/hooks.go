package droid

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	droidSettingsDirName = ".factory"
	droidHooksFileName   = "hooks.json"

	// droidHookCommandPrefix identifies the hook commands AO owns. Every managed
	// command starts with it, so install can skip duplicates and uninstall can
	// recognize AO entries by prefix without an embedded template to diff
	// against. The CLI dispatcher routes `ao hooks droid <event>` to the Droid
	// activity deriver.
	droidHookCommandPrefix = "ao hooks droid "
	droidHookTimeout       = 30
)

type droidMatcherGroup struct {
	// Matcher is a pointer so it round-trips exactly: SessionStart serializes
	// with its "startup" matcher; UserPromptSubmit/Stop/Notification/SessionEnd
	// omit it (Droid ignores matcher for those events). omitempty drops a nil
	// matcher on write.
	Matcher *string          `json:"matcher,omitempty"`
	Hooks   []droidHookEntry `json:"hooks"`
}

type droidHookEntry struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"`
}

// droidHookSpec describes one hook AO installs, defined in code rather than read
// from an embedded settings file.
type droidHookSpec struct {
	Event   string
	Matcher *string
	Command string
}

// droidStartupMatcher is referenced by pointer so SessionStart serializes with
// its "startup" source matcher.
var droidStartupMatcher = "startup"

// droidManagedHooks is the source of truth for the hooks AO installs:
// SessionStart (under the "startup" matcher), UserPromptSubmit, Stop,
// Notification, and SessionEnd. They report normalized activity-state signals
// back into AO's store (see DeriveActivityState). The non-SessionStart events
// carry no matcher: each installs once and fires for every sub-type, and the
// handler filters on the payload where it must.
var droidManagedHooks = []droidHookSpec{
	{Event: "SessionStart", Matcher: &droidStartupMatcher, Command: droidHookCommandPrefix + "session-start"},
	{Event: "UserPromptSubmit", Command: droidHookCommandPrefix + "user-prompt-submit"},
	{Event: "Stop", Command: droidHookCommandPrefix + "stop"},
	{Event: "Notification", Command: droidHookCommandPrefix + "notification"},
	{Event: "SessionEnd", Command: droidHookCommandPrefix + "session-end"},
}

// GetAgentHooks installs AO's Droid hooks into the worktree-local
// .factory/hooks.json file (the project-scope hooks config Droid reads). The
// hooks report normalized activity-state signals back into AO's store. Existing
// hooks and unrelated keys are preserved, and duplicate AO commands are not
// appended, so the install is idempotent.
func (p *Plugin) GetAgentHooks(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.WorkspacePath) == "" {
		return errors.New("droid.GetAgentHooks: WorkspacePath is required")
	}

	hooksPath := droidHooksPath(cfg.WorkspacePath)
	topLevel, rawHooks, err := readDroidHooks(hooksPath)
	if err != nil {
		return fmt.Errorf("droid.GetAgentHooks: %w", err)
	}

	byEvent := groupDroidHooksByEvent()
	events := make([]string, 0, len(byEvent))
	for event := range byEvent {
		events = append(events, event)
	}
	sort.Strings(events)
	for _, event := range events {
		specs := byEvent[event]
		var existingGroups []droidMatcherGroup
		if err := parseDroidHookType(rawHooks, event, &existingGroups); err != nil {
			return fmt.Errorf("droid.GetAgentHooks: %w", err)
		}
		for _, spec := range specs {
			if !droidHookCommandExists(existingGroups, spec.Command) {
				entry := droidHookEntry{Type: "command", Command: spec.Command, Timeout: droidHookTimeout}
				existingGroups = addDroidHook(existingGroups, entry, spec.Matcher)
			}
		}
		if err := marshalDroidHookType(rawHooks, event, existingGroups); err != nil {
			return fmt.Errorf("droid.GetAgentHooks: %w", err)
		}
	}

	if err := writeDroidHooks(hooksPath, topLevel, rawHooks); err != nil {
		return fmt.Errorf("droid.GetAgentHooks: %w", err)
	}
	return nil
}

// UninstallHooks removes AO's Droid hooks from the workspace-local
// .factory/hooks.json file, leaving user-defined hooks and unrelated keys
// untouched. A missing file is a no-op.
func (p *Plugin) UninstallHooks(ctx context.Context, workspacePath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(workspacePath) == "" {
		return errors.New("droid.UninstallHooks: workspacePath is required")
	}

	hooksPath := droidHooksPath(workspacePath)
	if _, err := os.Stat(hooksPath); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	topLevel, rawHooks, err := readDroidHooks(hooksPath)
	if err != nil {
		return fmt.Errorf("droid.UninstallHooks: %w", err)
	}

	for _, event := range droidManagedEvents() {
		var groups []droidMatcherGroup
		if err := parseDroidHookType(rawHooks, event, &groups); err != nil {
			return fmt.Errorf("droid.UninstallHooks: %w", err)
		}
		groups = removeDroidManagedHooks(groups)
		if err := marshalDroidHookType(rawHooks, event, groups); err != nil {
			return fmt.Errorf("droid.UninstallHooks: %w", err)
		}
	}

	if err := writeDroidHooks(hooksPath, topLevel, rawHooks); err != nil {
		return fmt.Errorf("droid.UninstallHooks: %w", err)
	}
	return nil
}

// AreHooksInstalled reports whether any AO Droid hook is present in the
// workspace-local hooks file. A missing file means none are installed.
func (p *Plugin) AreHooksInstalled(ctx context.Context, workspacePath string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if strings.TrimSpace(workspacePath) == "" {
		return false, errors.New("droid.AreHooksInstalled: workspacePath is required")
	}

	hooksPath := droidHooksPath(workspacePath)
	if _, err := os.Stat(hooksPath); errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	_, rawHooks, err := readDroidHooks(hooksPath)
	if err != nil {
		return false, fmt.Errorf("droid.AreHooksInstalled: %w", err)
	}

	for _, event := range droidManagedEvents() {
		var groups []droidMatcherGroup
		if err := parseDroidHookType(rawHooks, event, &groups); err != nil {
			return false, fmt.Errorf("droid.AreHooksInstalled: %w", err)
		}
		for _, group := range groups {
			for _, hook := range group.Hooks {
				if isDroidManagedHook(hook.Command) {
					return true, nil
				}
			}
		}
	}
	return false, nil
}

func droidHooksPath(workspacePath string) string {
	return filepath.Join(workspacePath, droidSettingsDirName, droidHooksFileName)
}

// readDroidHooks loads the hooks file into a top-level raw map plus the decoded
// "hooks" sub-map, preserving every key AO doesn't manage. A missing or empty
// file yields empty maps.
func readDroidHooks(hooksPath string) (topLevel, rawHooks map[string]json.RawMessage, err error) {
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

// writeDroidHooks folds rawHooks back into topLevel and writes the file. An
// empty hooks map drops the "hooks" key entirely.
func writeDroidHooks(hooksPath string, topLevel, rawHooks map[string]json.RawMessage) error {
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
		return fmt.Errorf("create hooks dir: %w", err)
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

// groupDroidHooksByEvent groups the managed hook specs by their Droid event so
// each event's array is rewritten once.
func groupDroidHooksByEvent() map[string][]droidHookSpec {
	byEvent := map[string][]droidHookSpec{}
	for _, spec := range droidManagedHooks {
		byEvent[spec.Event] = append(byEvent[spec.Event], spec)
	}
	return byEvent
}

// droidManagedEvents returns the distinct Droid events AO manages, in the order
// they first appear in droidManagedHooks.
func droidManagedEvents() []string {
	seen := map[string]bool{}
	events := make([]string, 0, len(droidManagedHooks))
	for _, spec := range droidManagedHooks {
		if !seen[spec.Event] {
			seen[spec.Event] = true
			events = append(events, spec.Event)
		}
	}
	return events
}

func isDroidManagedHook(command string) bool {
	return strings.HasPrefix(command, droidHookCommandPrefix)
}

// removeDroidManagedHooks strips AO hook entries from every group, dropping any
// group left without hooks so the event array doesn't accumulate empty matcher
// objects.
func removeDroidManagedHooks(groups []droidMatcherGroup) []droidMatcherGroup {
	result := make([]droidMatcherGroup, 0, len(groups))
	for _, group := range groups {
		kept := make([]droidHookEntry, 0, len(group.Hooks))
		for _, hook := range group.Hooks {
			if !isDroidManagedHook(hook.Command) {
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

func parseDroidHookType(rawHooks map[string]json.RawMessage, event string, target *[]droidMatcherGroup) error {
	data, ok := rawHooks[event]
	if !ok {
		return nil
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("parse %s hooks: %w", event, err)
	}
	return nil
}

func marshalDroidHookType(rawHooks map[string]json.RawMessage, event string, groups []droidMatcherGroup) error {
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

func droidHookCommandExists(groups []droidMatcherGroup, command string) bool {
	for _, group := range groups {
		for _, hook := range group.Hooks {
			if hook.Command == command {
				return true
			}
		}
	}
	return false
}

// addDroidHook appends hook to an existing group with the same matcher (so a
// SessionStart hook lands under its "startup" matcher), creating that group if
// none matches.
func addDroidHook(groups []droidMatcherGroup, hook droidHookEntry, matcher *string) []droidMatcherGroup {
	for i, group := range groups {
		if matchersEqual(group.Matcher, matcher) {
			groups[i].Hooks = append(groups[i].Hooks, hook)
			return groups
		}
	}
	return append(groups, droidMatcherGroup{Matcher: matcher, Hooks: []droidHookEntry{hook}})
}

func matchersEqual(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
