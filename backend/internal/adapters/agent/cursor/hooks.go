package cursor

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
	cursorHooksDirName  = ".cursor"
	cursorHooksFileName = "hooks.json"

	// cursorHooksSchemaVersion is the version Cursor's hooks.json declares. AO
	// only sets it when creating a fresh file; an existing version is preserved.
	cursorHooksSchemaVersion = 1

	// cursorHookCommandPrefix identifies the hook commands AO owns, so
	// install skips duplicates and uninstall recognizes AO entries by
	// prefix without an embedded template to diff against.
	cursorHookCommandPrefix = "ao hooks cursor "
)

// cursorHookFile is the on-disk shape of .cursor/hooks.json. It is used by tests
// to decode the written file. Cursor keys hooks by camelCase native event name;
// each value is an array of objects carrying a "command" string.
type cursorHookFile struct {
	Version int                          `json:"version"`
	Hooks   map[string][]cursorHookEntry `json:"hooks"`
}

type cursorHookEntry struct {
	Command string `json:"command"`
}

// cursorHookSpec describes one hook AO installs, defined in code rather than
// read from an embedded hooks file. Event is Cursor's native camelCase event
// name; Command is the AO sub-command dispatched when the hook fires.
type cursorHookSpec struct {
	Event   string
	Command string
}

// cursorManagedHooks is the source of truth for the hooks AO installs. The
// native-event → AO-subcommand contract is FIXED: the orchestrator's CLI hook
// dispatch and activity.go agree on the sub-command names.
var cursorManagedHooks = []cursorHookSpec{
	{Event: "sessionStart", Command: cursorHookCommandPrefix + "session-start"},
	{Event: "beforeSubmitPrompt", Command: cursorHookCommandPrefix + "user-prompt-submit"},
	{Event: "stop", Command: cursorHookCommandPrefix + "stop"},
	{Event: "beforeShellExecution", Command: cursorHookCommandPrefix + "permission-request"},
	{Event: "beforeMCPExecution", Command: cursorHookCommandPrefix + "permission-request"},
}

// GetAgentHooks installs AO's Cursor hooks into the worktree-local
// .cursor/hooks.json file. Existing hook entries are preserved and duplicate
// AO commands are not appended.
func (p *Plugin) GetAgentHooks(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.WorkspacePath) == "" {
		return errors.New("cursor.GetAgentHooks: WorkspacePath is required")
	}

	hooksPath := cursorHooksPath(cfg.WorkspacePath)
	topLevel, rawHooks, err := readCursorHooks(hooksPath)
	if err != nil {
		return fmt.Errorf("cursor.GetAgentHooks: %w", err)
	}

	for event, specs := range groupCursorHooksByEvent() {
		var existing []cursorHookEntry
		if err := parseCursorHookEvent(rawHooks, event, &existing); err != nil {
			return fmt.Errorf("cursor.GetAgentHooks: %w", err)
		}
		for _, spec := range specs {
			if !cursorHookCommandExists(existing, spec.Command) {
				existing = append(existing, cursorHookEntry{Command: spec.Command})
			}
		}
		if err := marshalCursorHookEvent(rawHooks, event, existing); err != nil {
			return fmt.Errorf("cursor.GetAgentHooks: %w", err)
		}
	}

	if err := writeCursorHooks(hooksPath, topLevel, rawHooks); err != nil {
		return fmt.Errorf("cursor.GetAgentHooks: %w", err)
	}
	return nil
}

// UninstallHooks removes AO's Cursor hooks from the workspace-local
// .cursor/hooks.json file, leaving user-defined hooks untouched. A missing file
// is a no-op.
func (p *Plugin) UninstallHooks(ctx context.Context, workspacePath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(workspacePath) == "" {
		return errors.New("cursor.UninstallHooks: workspacePath is required")
	}

	hooksPath := cursorHooksPath(workspacePath)
	if _, err := os.Stat(hooksPath); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	topLevel, rawHooks, err := readCursorHooks(hooksPath)
	if err != nil {
		return fmt.Errorf("cursor.UninstallHooks: %w", err)
	}

	for _, event := range cursorManagedEvents() {
		var entries []cursorHookEntry
		if err := parseCursorHookEvent(rawHooks, event, &entries); err != nil {
			return fmt.Errorf("cursor.UninstallHooks: %w", err)
		}
		entries = removeCursorManagedHooks(entries)
		if err := marshalCursorHookEvent(rawHooks, event, entries); err != nil {
			return fmt.Errorf("cursor.UninstallHooks: %w", err)
		}
	}

	if err := writeCursorHooks(hooksPath, topLevel, rawHooks); err != nil {
		return fmt.Errorf("cursor.UninstallHooks: %w", err)
	}
	return nil
}

// AreHooksInstalled reports whether any AO Cursor hook is present in the
// workspace-local hooks file. A missing file means none are installed.
func (p *Plugin) AreHooksInstalled(ctx context.Context, workspacePath string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if strings.TrimSpace(workspacePath) == "" {
		return false, errors.New("cursor.AreHooksInstalled: workspacePath is required")
	}

	hooksPath := cursorHooksPath(workspacePath)
	if _, err := os.Stat(hooksPath); errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	_, rawHooks, err := readCursorHooks(hooksPath)
	if err != nil {
		return false, fmt.Errorf("cursor.AreHooksInstalled: %w", err)
	}

	for _, event := range cursorManagedEvents() {
		var entries []cursorHookEntry
		if err := parseCursorHookEvent(rawHooks, event, &entries); err != nil {
			return false, fmt.Errorf("cursor.AreHooksInstalled: %w", err)
		}
		for _, hook := range entries {
			if isCursorManagedHook(hook.Command) {
				return true, nil
			}
		}
	}
	return false, nil
}

func cursorHooksPath(workspacePath string) string {
	return filepath.Join(workspacePath, cursorHooksDirName, cursorHooksFileName)
}

// readCursorHooks loads the hooks file into a top-level raw map plus the decoded
// "hooks" sub-map, preserving keys AO doesn't manage (e.g. "version"). A missing
// or empty file yields empty maps.
func readCursorHooks(hooksPath string) (topLevel, rawHooks map[string]json.RawMessage, err error) {
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

// writeCursorHooks folds rawHooks back into topLevel and writes the file. An
// empty hooks map drops the "hooks" key entirely. A "version" key is ensured so
// a freshly created file declares the schema version Cursor expects, while an
// existing version (preserved in topLevel) is left untouched.
func writeCursorHooks(hooksPath string, topLevel, rawHooks map[string]json.RawMessage) error {
	if len(rawHooks) == 0 {
		delete(topLevel, "hooks")
	} else {
		hooksJSON, err := json.Marshal(rawHooks)
		if err != nil {
			return fmt.Errorf("encode hooks: %w", err)
		}
		topLevel["hooks"] = hooksJSON
		if _, ok := topLevel["version"]; !ok {
			versionJSON, err := json.Marshal(cursorHooksSchemaVersion)
			if err != nil {
				return fmt.Errorf("encode version: %w", err)
			}
			topLevel["version"] = versionJSON
		}
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

// groupCursorHooksByEvent groups the managed hook specs by their Cursor event so
// each event's array is rewritten once.
func groupCursorHooksByEvent() map[string][]cursorHookSpec {
	byEvent := map[string][]cursorHookSpec{}
	for _, spec := range cursorManagedHooks {
		byEvent[spec.Event] = append(byEvent[spec.Event], spec)
	}
	return byEvent
}

// cursorManagedEvents returns the distinct Cursor events AO manages, in the
// order they first appear in cursorManagedHooks.
func cursorManagedEvents() []string {
	seen := map[string]bool{}
	events := make([]string, 0, len(cursorManagedHooks))
	for _, spec := range cursorManagedHooks {
		if !seen[spec.Event] {
			seen[spec.Event] = true
			events = append(events, spec.Event)
		}
	}
	return events
}

func isCursorManagedHook(command string) bool {
	return strings.HasPrefix(command, cursorHookCommandPrefix)
}

// removeCursorManagedHooks strips AO hook entries from an event's array,
// preserving user-defined entries.
func removeCursorManagedHooks(entries []cursorHookEntry) []cursorHookEntry {
	kept := make([]cursorHookEntry, 0, len(entries))
	for _, hook := range entries {
		if !isCursorManagedHook(hook.Command) {
			kept = append(kept, hook)
		}
	}
	return kept
}

func parseCursorHookEvent(rawHooks map[string]json.RawMessage, event string, target *[]cursorHookEntry) error {
	data, ok := rawHooks[event]
	if !ok {
		return nil
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("parse %s hooks: %w", event, err)
	}
	return nil
}

func marshalCursorHookEvent(rawHooks map[string]json.RawMessage, event string, entries []cursorHookEntry) error {
	if len(entries) == 0 {
		delete(rawHooks, event)
		return nil
	}
	data, err := json.Marshal(entries)
	if err != nil {
		return fmt.Errorf("encode %s hooks: %w", event, err)
	}
	rawHooks[event] = data
	return nil
}

func cursorHookCommandExists(entries []cursorHookEntry, command string) bool {
	for _, hook := range entries {
		if hook.Command == command {
			return true
		}
	}
	return false
}
