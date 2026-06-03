package opencode

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "embed"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	// opencode scans both `.opencode/plugin/` and `.opencode/plugins/` for
	// `*.js`/`*.ts` files (see opencode's ConfigPlugin glob
	// "{plugin,plugins}/*.{ts,js}"). AO writes the plural `plugins/`, matching
	// the directory the upstream opencode tooling (and the entire-cli reference
	// integration) uses.
	opencodePluginDirName = ".opencode"
	opencodePluginSubDir  = "plugins"

	// opencodePluginFileName is the AO-owned plugin file. AO fully owns this
	// filename: install overwrites it and uninstall deletes it (guarded by the
	// sentinel), so user-authored plugins in other files are never touched.
	// It is TypeScript (opencode runs on Bun); the file's only import is a
	// type-only import, which Bun erases at runtime.
	opencodePluginFileName = "ao-activity.ts"

	// opencodePluginSentinel marks the file as AO-managed. AreHooksInstalled and
	// UninstallHooks key off it so AO never deletes a user file that happens to
	// share the name. It must appear verbatim in the embedded plugin source.
	opencodePluginSentinel = "agent-orchestrator: managed opencode activity plugin"

	// opencodeHookCommandPrefix identifies the hook commands AO owns. The
	// embedded plugin shells `ao hooks opencode <event>`; this prefix is the
	// shared contract with the (forthcoming) `ao hooks` CLI and is asserted by
	// tests so the plugin can't silently drift away from it.
	opencodeHookCommandPrefix = "ao hooks opencode "
)

// opencodePluginSource is the AO-managed opencode plugin, embedded so it ships
// inside the binary and is written verbatim into a session's worktree on hook
// install. It is a real, lintable source file under assets/ rather than a Go
// string literal because it is opencode plugin source code, not a data
// structure AO assembles (the way it builds Codex/Claude hook JSON).
//
//go:embed assets/ao-activity.ts
var opencodePluginSource string

// opencodeManagedEvents are the three normalized activity events the embedded
// plugin reports. They are defined here (not parsed from the file) so tests can
// assert the plugin wires every one via the `ao hooks opencode <event>` command.
var opencodeManagedEvents = []string{"session-start", "user-prompt-submit", "stop"}

// GetAgentHooks installs AO's opencode activity plugin into the worktree-local
// .opencode/plugins/ directory. Unlike Claude Code and Codex, opencode has no
// native command-hook config to merge into; its only lifecycle-extensibility
// surface is a JS/TS plugin. AO therefore writes a dedicated, AO-owned plugin
// file. The write is atomic and idempotent: re-installing overwrites AO's own
// file with identical content. It refuses to overwrite a file that is NOT
// AO-managed (no sentinel), so a user plugin that happens to occupy our path is
// never silently destroyed — install fails loudly instead.
func (p *Plugin) GetAgentHooks(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.WorkspacePath) == "" {
		return errors.New("opencode.GetAgentHooks: WorkspacePath is required")
	}

	pluginPath := opencodePluginPath(cfg.WorkspacePath)
	// Guard against clobbering a user file at our path: overwrite only when the
	// target is absent or already AO-managed. A foreign file is a loud error,
	// not silent data loss (uninstall is sentinel-guarded the same way).
	if _, err := os.Stat(pluginPath); err == nil {
		managed, err := isAOManagedPlugin(pluginPath)
		if err != nil {
			return fmt.Errorf("opencode.GetAgentHooks: %w", err)
		}
		if !managed {
			return fmt.Errorf("opencode.GetAgentHooks: refusing to overwrite non-AO file at %s — move it so AO can install its plugin", pluginPath)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("opencode.GetAgentHooks: stat plugin: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(pluginPath), 0o750); err != nil {
		return fmt.Errorf("opencode.GetAgentHooks: create plugin dir: %w", err)
	}
	if err := atomicWriteFile(pluginPath, []byte(opencodePluginSource), 0o600); err != nil {
		return fmt.Errorf("opencode.GetAgentHooks: write plugin: %w", err)
	}
	return nil
}

// UninstallHooks removes AO's opencode plugin from the workspace-local
// .opencode/plugins/ directory. It deletes the file only when it carries the AO
// sentinel, so a user file that happens to share the name is left in place. A
// missing file is a no-op.
func (p *Plugin) UninstallHooks(ctx context.Context, workspacePath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(workspacePath) == "" {
		return errors.New("opencode.UninstallHooks: workspacePath is required")
	}

	pluginPath := opencodePluginPath(workspacePath)
	managed, err := isAOManagedPlugin(pluginPath)
	if err != nil {
		return fmt.Errorf("opencode.UninstallHooks: %w", err)
	}
	if !managed {
		return nil
	}
	if err := os.Remove(pluginPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("opencode.UninstallHooks: remove plugin: %w", err)
	}
	return nil
}

// AreHooksInstalled reports whether AO's opencode plugin is present in the
// workspace-local plugin dir. A missing file, or a same-named file without the
// AO sentinel, means none are installed.
func (p *Plugin) AreHooksInstalled(ctx context.Context, workspacePath string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if strings.TrimSpace(workspacePath) == "" {
		return false, errors.New("opencode.AreHooksInstalled: workspacePath is required")
	}
	managed, err := isAOManagedPlugin(opencodePluginPath(workspacePath))
	if err != nil {
		return false, fmt.Errorf("opencode.AreHooksInstalled: %w", err)
	}
	return managed, nil
}

func opencodePluginPath(workspacePath string) string {
	return filepath.Join(workspacePath, opencodePluginDirName, opencodePluginSubDir, opencodePluginFileName)
}

// isAOManagedPlugin reports whether the file at path exists and carries the AO
// sentinel. A missing file yields (false, nil).
func isAOManagedPlugin(path string) (bool, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path built from caller-owned workspace dir
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	return strings.Contains(string(data), opencodePluginSentinel), nil
}

// atomicWriteFile writes data to path via a temp file + rename, so a crash mid-
// write can't leave a truncated plugin file that opencode then fails to import
// (silently disabling activity reporting).
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
