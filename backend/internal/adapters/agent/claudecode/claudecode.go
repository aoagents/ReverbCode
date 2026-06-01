// Package claudecode implements the Claude Code agent adapter.
//
// It builds the argv to launch `claude` as an interactive session inside a
// session's worktree, installs worktree-local hooks that report normalized
// session metadata (native id, title, summary) back into Better-AO's store,
// and supports resume: GetLaunchCommand pins a stable `--session-id` so
// GetRestoreCommand can rebuild `claude --resume <uuid>`. SessionInfo reads the
// hook-captured metadata from the store — it does not parse transcripts.
// GetConfigSpec remains a no-op (no agent-specific config keys yet).
//
// Claude Code starts an interactive session by default (no -p/--print), which
// is exactly what better-ao wants: a live agent the user can attach to in the
// browser terminal or via `zellij attach`. The initial task prompt is passed
// as the positional argument; the orchestrator system prompt (if any) is
// appended to Claude's default system prompt so its built-in coding
// instructions are preserved.
package claudecode

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent"
)

const (
	// adapterID is the registry id and the value users pass to
	// `better-ao spawn --agent`.
	adapterID = "claude-code"

	// Normalized session-metadata keys the Claude Code hooks persist into the
	// Better-AO session store and SessionInfo reads back. Shared vocabulary
	// with the Codex adapter so the dashboard treats every agent uniformly.
	// agentSessionId is also the preferred restore id.
	claudeAgentSessionIDMetadataKey = "agentSessionId"
	claudeTitleMetadataKey          = "title"
	claudeSummaryMetadataKey        = "summary"
)

// claudeSessionNamespace seeds the UUIDv5 derivation that maps a better-ao
// session id onto a stable Claude Code `--session-id`. A fixed namespace makes
// the mapping deterministic, so GetLaunchCommand (which pins --session-id at
// launch) and GetRestoreCommand (which recomputes it as a fallback for
// pre-hook sessions) agree without persisting anything.
var claudeSessionNamespace = uuid.MustParse("a1f0c3d2-7b54-4e96-8a2b-0d9e1f2a3b4c")

type Plugin struct {
	binaryMu       sync.Mutex
	resolvedBinary string
}

func New() *Plugin {
	return &Plugin{}
}

var _ adapters.Adapter = (*Plugin)(nil)
var _ agent.Agent = (*Plugin)(nil)

func (p *Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{
		ID:          adapterID,
		Name:        "Claude Code",
		Description: "Run Claude Code worker sessions.",
		Version:     "0.0.1",
		Capabilities: []adapters.Capability{
			adapters.CapabilityAgent,
		},
	}
}

func (p *Plugin) GetConfigSpec(ctx context.Context) (agent.ConfigSpec, error) {
	if err := ctx.Err(); err != nil {
		return agent.ConfigSpec{}, err
	}
	return agent.ConfigSpec{}, nil
}

// GetLaunchCommand builds the argv to start an interactive Claude Code
// session. Shape:
//
//	claude [--session-id <uuid>] \
//	       [--permission-mode <mode>] \
//	       [--append-system-prompt <system prompt>] \
//	       [-- <prompt>]
//
// --session-id pins Claude's native session UUID to a value derived from the
// better-ao session id, so the session is resumable later (see
// GetRestoreCommand) and its transcript is locatable (see SessionInfo) without
// a separate capture step.
//
// <mode> is acceptEdits, auto, or bypassPermissions. better-ao's "default"
// mode emits no --permission-mode flag, so Claude's TUI resolves the starting
// mode from ~/.claude/settings.json exactly as a normal launch.
//
// The prompt is passed after `--` so a prompt beginning with "-" is not
// mistaken for a flag.
func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg agent.LaunchConfig) (cmd []string, err error) {
	binary, err := p.claudeBinary(ctx)
	if err != nil {
		return nil, err
	}

	cmd = []string{binary}
	if cfg.SessionID != "" {
		cmd = append(cmd, "--session-id", claudeSessionUUID(cfg.SessionID))
	}
	appendPermissionFlags(&cmd, cfg.Permissions)

	systemPrompt, err := resolveSystemPrompt(cfg)
	if err != nil {
		return nil, err
	}
	if systemPrompt != "" {
		// Append rather than replace: Claude Code's default system prompt
		// carries its tool-use and coding instructions, which we want to
		// keep. The orchestrator prompt layers on top.
		cmd = append(cmd, "--append-system-prompt", systemPrompt)
	}

	if cfg.Prompt != "" {
		cmd = append(cmd, "--", cfg.Prompt)
	}

	return cmd, nil
}

func (p *Plugin) GetPromptDeliveryStrategy(ctx context.Context, cfg agent.LaunchConfig) (agent.PromptDeliveryStrategy, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return agent.PromptDeliveryInCommand, nil
}

// PreLaunch is an optional capability the spawn engine invokes (via type
// assertion) immediately before creating the session. Claude Code shows a
// blocking "do you trust this folder?" dialog the first time it runs in any
// directory. Every better-ao worktree is a fresh path, so without this the
// agent would hang at that prompt with no one to answer it.
//
// A better-ao worktree is derived from the repo the user is already running
// better-ao in, so it is inherently trusted. PreLaunch records that trust in
// ~/.claude.json before launch, additively and atomically, so it cannot
// clobber a concurrently-running Claude instance's config.
func (p *Plugin) PreLaunch(ctx context.Context, cfg agent.LaunchConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if cfg.WorkspacePath == "" {
		return nil
	}
	cfgPath, err := claudeConfigPath()
	if err != nil {
		return err
	}
	return ensureWorkspaceTrusted(cfgPath, cfg.WorkspacePath)
}

// GetRestoreCommand rebuilds the argv that continues an existing Claude Code
// session: `claude [--permission-mode <mode>] --resume <agentSessionId>`. It
// prefers the hook-captured native session id from
// cfg.Session.Metadata["agentSessionId"]; for sessions created before hooks
// captured it, it falls back to the deterministic UUID better-ao pins via
// --session-id at launch. ok is false only when neither is available, so the
// caller fresh-spawns. The command re-applies the permission mode (resume
// otherwise reverts to the configured default) but not the prompt/system
// prompt, which the session already carries.
func (p *Plugin) GetRestoreCommand(ctx context.Context, cfg agent.RestoreConfig) (cmd []string, ok bool, err error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}

	sessionID := strings.TrimSpace(cfg.Session.Metadata[claudeAgentSessionIDMetadataKey])
	if sessionID == "" && cfg.Session.ID != "" {
		// Explicit fallback for pre-hook sessions: the id better-ao
		// deterministically pinned via --session-id at launch.
		sessionID = claudeSessionUUID(cfg.Session.ID)
	}
	if sessionID == "" {
		return nil, false, nil
	}

	binary, err := p.claudeBinary(ctx)
	if err != nil {
		return nil, false, err
	}
	cmd = []string{binary}
	appendPermissionFlags(&cmd, cfg.Permissions)
	cmd = append(cmd, "--resume", sessionID)
	return cmd, true, nil
}

// SessionInfo surfaces the normalized session metadata that the Claude Code
// hooks persisted into Better-AO's store: the native session id, the title (the
// first user prompt), and the summary (the final assistant message). It reads
// only from session.Metadata — never from transcript files — and returns
// ok=false when none of those fields are present. Metadata is intentionally nil:
// there is no Claude-specific field callers need beyond the normalized ones.
func (p *Plugin) SessionInfo(ctx context.Context, session agent.SessionRef) (agent.SessionInfo, bool, error) {
	if err := ctx.Err(); err != nil {
		return agent.SessionInfo{}, false, err
	}
	info := agent.SessionInfo{
		AgentSessionID: session.Metadata[claudeAgentSessionIDMetadataKey],
		Title:          session.Metadata[claudeTitleMetadataKey],
		Summary:        session.Metadata[claudeSummaryMetadataKey],
	}
	if info.AgentSessionID == "" && info.Title == "" && info.Summary == "" {
		return agent.SessionInfo{}, false, nil
	}
	return info, true, nil
}

// claudeSessionUUID maps a better-ao session id onto a stable Claude Code
// session UUID via UUIDv5 over a fixed namespace, so the same better-ao session
// always resolves to the same Claude session.
func claudeSessionUUID(betterAoSessionID string) string {
	return uuid.NewSHA1(claudeSessionNamespace, []byte(betterAoSessionID)).String()
}

// resolveSystemPrompt returns the system prompt text to append, preferring
// SystemPromptFile (read from disk) over an inline SystemPrompt.
func resolveSystemPrompt(cfg agent.LaunchConfig) (string, error) {
	if cfg.SystemPromptFile != "" {
		data, err := os.ReadFile(cfg.SystemPromptFile)
		if err != nil {
			return "", fmt.Errorf("claude-code: read system prompt file: %w", err)
		}
		return strings.TrimRight(string(data), "\n"), nil
	}
	return cfg.SystemPrompt, nil
}

// appendPermissionFlags maps better-ao's permission modes onto Claude Code's
// --permission-mode values:
//   - default            → no flag. Claude's TUI resolves the starting mode
//     from ~/.claude/settings.json (defaultMode), exactly as a normal launch.
//   - accept-edits       → --permission-mode acceptEdits (auto-accept edits +
//     safe filesystem bash; still prompts for network/system bash, MCP, web)
//   - auto               → --permission-mode auto (classifier-gated
//     auto-approval; auto-runs what a safety model deems safe)
//   - bypass-permissions → --permission-mode bypassPermissions (skip all
//     checks; equivalent to --dangerously-skip-permissions)
//
// Empty/unrecognized normalizes to default, so no flag is emitted.
func appendPermissionFlags(cmd *[]string, permissions agent.PermissionMode) {
	switch normalizePermissionMode(permissions) {
	case agent.PermissionModeDefault:
		// No flag: defer to the user's settings.json defaultMode.
	case agent.PermissionModeAcceptEdits:
		*cmd = append(*cmd, "--permission-mode", "acceptEdits")
	case agent.PermissionModeAuto:
		*cmd = append(*cmd, "--permission-mode", "auto")
	case agent.PermissionModeBypassPermissions:
		*cmd = append(*cmd, "--permission-mode", "bypassPermissions")
	}
}

func normalizePermissionMode(mode agent.PermissionMode) agent.PermissionMode {
	switch mode {
	case agent.PermissionModeDefault,
		agent.PermissionModeAcceptEdits,
		agent.PermissionModeAuto,
		agent.PermissionModeBypassPermissions:
		return mode
	default:
		// Empty or unrecognized: defer to settings.json (no flag).
		return agent.PermissionModeDefault
	}
}

// ResolveClaudeBinary finds the `claude` binary, searching PATH then a few
// well-known install locations (the native installer's ~/.local/bin, npm
// global, Homebrew). Returns "claude" as a last resort so callers get a
// clear "command not found" rather than an empty argv.
func ResolveClaudeBinary(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	if runtime.GOOS == "windows" {
		for _, name := range []string{"claude.cmd", "claude.exe", "claude"} {
			if path, err := exec.LookPath(name); err == nil && path != "" {
				return path, nil
			}
		}
		candidates := []string{}
		if appData := os.Getenv("APPDATA"); appData != "" {
			candidates = append(candidates,
				filepath.Join(appData, "npm", "claude.cmd"),
				filepath.Join(appData, "npm", "claude.exe"),
			)
		}
		for _, candidate := range candidates {
			if fileExists(candidate) {
				return candidate, nil
			}
		}
		return "claude", nil
	}

	if path, err := exec.LookPath("claude"); err == nil && path != "" {
		return path, nil
	}

	candidates := []string{
		"/usr/local/bin/claude",
		"/opt/homebrew/bin/claude",
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates,
			filepath.Join(home, ".local", "bin", "claude"),
			filepath.Join(home, ".npm", "bin", "claude"),
			filepath.Join(home, ".claude", "local", "claude"),
		)
	}
	for _, candidate := range candidates {
		if fileExists(candidate) {
			return candidate, nil
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
	}

	return "claude", nil
}

func (p *Plugin) claudeBinary(ctx context.Context) (string, error) {
	p.binaryMu.Lock()
	defer p.binaryMu.Unlock()

	if p.resolvedBinary != "" {
		return p.resolvedBinary, nil
	}

	binary, err := ResolveClaudeBinary(ctx)
	if err != nil {
		return "", err
	}
	p.resolvedBinary = binary
	return binary, nil
}

// claudeConfigPath returns the path to Claude Code's global config file,
// ~/.claude.json.
func claudeConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("claude-code: resolve home directory: %w", err)
	}
	return filepath.Join(home, ".claude.json"), nil
}

// ensureWorkspaceTrusted records workspacePath as trusted in Claude Code's
// config so the interactive trust dialog does not block a spawned session.
//
// It is additive and concurrency-safe: it reads the existing config, sets
// only projects[workspacePath].hasTrustDialogAccepted = true (preserving the
// rest of the entry and every other project), and writes back via a
// temp-file + atomic rename. If the path is already trusted, it makes no
// write at all. A missing config file is treated as an empty one.
func ensureWorkspaceTrusted(configPath, workspacePath string) error {
	root := map[string]any{}
	data, err := os.ReadFile(configPath)
	switch {
	case err == nil:
		if len(data) > 0 {
			if err := json.Unmarshal(data, &root); err != nil {
				return fmt.Errorf("claude-code: parse %s: %w", configPath, err)
			}
		}
	case os.IsNotExist(err):
		// Treat as empty config; we'll create it.
	default:
		return fmt.Errorf("claude-code: read %s: %w", configPath, err)
	}

	projects, _ := root["projects"].(map[string]any)
	if projects == nil {
		projects = map[string]any{}
		root["projects"] = projects
	}

	entry, _ := projects[workspacePath].(map[string]any)
	if entry == nil {
		entry = map[string]any{}
		projects[workspacePath] = entry
	}

	if trusted, ok := entry["hasTrustDialogAccepted"].(bool); ok && trusted {
		// Already trusted — no write needed, so no race window at all.
		return nil
	}
	entry["hasTrustDialogAccepted"] = true

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return fmt.Errorf("claude-code: encode %s: %w", configPath, err)
	}

	// Atomic write: temp file in the same directory, then rename. Matches
	// how Claude Code itself updates this file, so concurrent updates are
	// last-writer-wins rather than corrupting.
	dir := filepath.Dir(configPath)
	tmp, err := os.CreateTemp(dir, ".claude.json.tmp-*")
	if err != nil {
		return fmt.Errorf("claude-code: create temp config: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once renamed

	if _, err := tmp.Write(out); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("claude-code: write temp config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("claude-code: close temp config: %w", err)
	}
	if err := os.Rename(tmpName, configPath); err != nil {
		return fmt.Errorf("claude-code: replace config: %w", err)
	}
	return nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
