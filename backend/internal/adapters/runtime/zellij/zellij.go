// Package zellij implements ports.Runtime using Zellij sessions.
package zellij

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/agentlaunch"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	defaultTimeout        = 5 * time.Second
	defaultWindowsTimeout = 30 * time.Second
	defaultZellijTerm     = "xterm-256color"
	defaultZellijColor    = "truecolor"
	minMajor              = 0
	minMinor              = 44
	minPatch              = 3
)

var sessionIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
var paneIDPattern = regexp.MustCompile(`^terminal_\d+$`)

var getenv = os.Getenv
var lookPath = exec.LookPath
var fileExists = func(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// Options configures a zellij Runtime; every field has a sensible default
// (see New), so the zero value is usable.
type Options struct {
	Binary         string
	Timeout        time.Duration
	Shell          string
	SocketDir      string
	ConfigDir      string
	ChunkSize      int
	LauncherBinary string
}

// Runtime runs agent sessions inside zellij sessions, driving them via the
// zellij CLI. It implements ports.Runtime.
type Runtime struct {
	binary    string
	timeout   time.Duration
	shell     string
	socketDir string
	configDir string
	chunkSize int
	launcher  string
	runner    runner
}

var _ ports.Runtime = (*Runtime)(nil)

// DefaultSocketDir returns a short, stable ZELLIJ_SOCKET_DIR for AO's daemon.
// zellij's own default lives under $TMPDIR (long on macOS), which leaves almost
// none of the ~103-byte unix-socket-path budget for the session name — a long
// session id then fails with "session name must be less than 0 characters". A
// short dir restores ample budget. Empty on Windows, where zellij is not used.
// Pure: callers that run zellij should MkdirAll the result.
func DefaultSocketDir() string {
	if runtime.GOOS == "windows" {
		return ""
	}
	return "/tmp/ao-zellij-" + strconv.Itoa(os.Getuid())
}

type runner interface {
	Run(ctx context.Context, env []string, name string, args ...string) ([]byte, error)
	Start(env []string, name string, args ...string) error
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = zellijCommandEnv(os.Environ(), env)
	return cmd.CombinedOutput()
}

func (execRunner) Start(env []string, name string, args ...string) error {
	return startBackgroundProcess(zellijCommandEnv(os.Environ(), env), name, args...)
}

// New builds a zellij Runtime, filling unset Options with defaults: binary
// "zellij", shell from $SHELL (else /bin/sh, or powershell.exe on Windows), and
// the default timeout and output chunk size.
func New(opts Options) *Runtime {
	binary := opts.Binary
	if binary == "" {
		binary = defaultBinary()
	}
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = defaultCommandTimeout()
	}
	shellPath := opts.Shell
	if shellPath == "" {
		shellPath = os.Getenv("SHELL")
	}
	if shellPath == "" {
		if runtime.GOOS == "windows" {
			shellPath = "powershell.exe"
		} else {
			shellPath = "/bin/sh"
		}
	}
	chunkSize := opts.ChunkSize
	if chunkSize <= 0 {
		chunkSize = defaultChunkBytes
	}
	launcher := opts.LauncherBinary
	if launcher == "" {
		launcher = defaultLauncherBinary()
	}
	return &Runtime{binary: binary, timeout: timeout, shell: shellPath, socketDir: opts.SocketDir, configDir: opts.ConfigDir, chunkSize: chunkSize, launcher: launcher, runner: execRunner{}}
}

// defaultLauncherBinary returns the path used by the zellij Windows codepath
// to invoke the `ao launch` trampoline. On Windows the agent's argv is
// persisted to a temp spec file (see agentlaunch); zellij then runs this
// binary with `launch` and it execs the real agent. Falls back to plain "ao"
// if the daemon binary path cannot be resolved (PATH lookup at runtime).
func defaultLauncherBinary() string {
	path, err := os.Executable()
	if err == nil && isLauncherBinary(path) {
		return path
	}
	return "ao"
}

func isLauncherBinary(path string) bool {
	name := strings.ToLower(filepath.Base(path))
	if runtime.GOOS == "windows" {
		name = strings.TrimSuffix(name, ".exe")
	}
	return name == "ao"
}

func defaultCommandTimeout() time.Duration {
	if runtime.GOOS == "windows" {
		return defaultWindowsTimeout
	}
	return defaultTimeout
}

func defaultBinary() string {
	names := []string{"zellij"}
	if runtime.GOOS == "windows" {
		names = []string{"zellij.exe", "zellij"}
	}
	for _, name := range names {
		if path, err := lookPath(name); err == nil && path != "" {
			return path
		}
	}
	if runtime.GOOS == "windows" {
		for _, candidate := range windowsZellijCandidates() {
			if fileExists(candidate) {
				return candidate
			}
		}
	}
	return "zellij"
}

func windowsZellijCandidates() []string {
	candidates := []string{}
	if localAppData := getenv("LOCALAPPDATA"); localAppData != "" {
		candidates = append(candidates, filepath.Join(localAppData, "Programs", "zellij", "zellij.exe"))
	}
	for _, key := range []string{"ProgramFiles", "ProgramFiles(x86)"} {
		if dir := getenv(key); dir != "" {
			candidates = append(candidates,
				filepath.Join(dir, "zellij", "zellij.exe"),
				filepath.Join(dir, "Zellij", "zellij.exe"),
			)
		}
	}
	return candidates
}

// Create starts a new zellij session in the workspace, running the agent's
// launch command, and returns a handle to it.
func (r *Runtime) Create(ctx context.Context, cfg ports.RuntimeConfig) (ports.RuntimeHandle, error) {
	id, err := zellijSessionName(cfg.SessionID)
	if err != nil {
		return ports.RuntimeHandle{}, err
	}
	if cfg.WorkspacePath == "" {
		return ports.RuntimeHandle{}, errors.New("zellij runtime: workspace path is required")
	}
	if len(cfg.Argv) == 0 {
		return ports.RuntimeHandle{}, errors.New("zellij runtime: launch command is required")
	}
	if err := validateEnvKeys(cfg.Env); err != nil {
		return ports.RuntimeHandle{}, err
	}
	if err := r.ensureSupportedVersion(ctx); err != nil {
		return ports.RuntimeHandle{}, err
	}
	// Zellij keeps exited sessions in a resurrection cache. A previous partial
	// spawn can therefore make `attach --create-background` fail with "Session
	// already exists" even though AO has no usable runtime handle. Clear any
	// same-name runtime state before creating the new AO-owned session.
	if err := r.Destroy(ctx, ports.RuntimeHandle{ID: id}); err != nil {
		return ports.RuntimeHandle{}, err
	}

	layoutPath, launchEnv, cleanupLaunchSpec, err := r.writeLayout(cfg)
	if err != nil {
		return ports.RuntimeHandle{}, err
	}
	defer func() { _ = os.Remove(layoutPath) }()
	cleanupOnFailure := true
	defer func() {
		if cleanupOnFailure && cleanupLaunchSpec != nil {
			cleanupLaunchSpec()
		}
	}()

	if err := r.createSession(ctx, id, layoutPath, launchEnv); err != nil {
		return ports.RuntimeHandle{}, fmt.Errorf("zellij runtime: create session %s: %w", id, err)
	}
	paneID, err := r.findAgentPane(ctx, id)
	if err != nil {
		_ = r.Destroy(context.Background(), ports.RuntimeHandle{ID: id})
		return ports.RuntimeHandle{}, err
	}
	if err := r.waitForPaneReady(ctx, id, paneID); err != nil {
		_ = r.Destroy(context.Background(), ports.RuntimeHandle{ID: id})
		return ports.RuntimeHandle{}, err
	}
	handle := ports.RuntimeHandle{ID: handleIDValue(id, paneID)}
	alive, err := r.IsAlive(ctx, handle)
	if err != nil {
		_ = r.Destroy(context.Background(), handle)
		return ports.RuntimeHandle{}, fmt.Errorf("zellij runtime: verify session %s: %w", id, err)
	}
	if !alive {
		_ = r.Destroy(context.Background(), handle)
		return ports.RuntimeHandle{}, fmt.Errorf("zellij runtime: session %s exited before ready", id)
	}
	cleanupOnFailure = false
	return handle, nil
}

// createSession runs `zellij attach --create-background`. On Windows we spawn
// it via runner.Start (fire-and-forget) because the inherited daemon stdio
// confuses zellij's own readiness probe; on Unix we keep the synchronous run.
func (r *Runtime) createSession(ctx context.Context, id, layoutPath string, env map[string]string) error {
	args := createSessionArgs(id, layoutPath)
	if runtime.GOOS != "windows" {
		_, err := r.run(ctx, args...)
		return err
	}
	return r.startWithEnv(env, args...)
}

// Destroy kills the handle's zellij session and deletes its serialized state,
// so the session can never be resurrected by a later `zellij attach`. An
// already-gone session is treated as success.
func (r *Runtime) Destroy(ctx context.Context, handle ports.RuntimeHandle) error {
	id, _, err := handleID(handle)
	if err != nil {
		return err
	}
	out, err := r.run(ctx, deleteSessionArgs(id)...)
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && deleteSessionMissingOutput(string(out)) {
			return nil
		}
		return fmt.Errorf("zellij runtime: destroy session %s: %w", id, err)
	}
	return nil
}

// SendMessage pastes a message into the session's pane (chunked) and presses
// Enter to submit it.
func (r *Runtime) SendMessage(ctx context.Context, handle ports.RuntimeHandle, message string) error {
	id, paneID, err := handleID(handle)
	if err != nil {
		return err
	}
	for _, chunk := range chunks(message, r.chunkSize) {
		if _, err := r.run(ctx, pasteArgs(id, paneID, chunk)...); err != nil {
			return fmt.Errorf("zellij runtime: paste message %s/%s: %w", id, paneID, err)
		}
	}
	if _, err := r.run(ctx, sendEnterArgs(id, paneID)...); err != nil {
		return fmt.Errorf("zellij runtime: send enter %s/%s: %w", id, paneID, err)
	}
	return nil
}

// GetOutput returns the last `lines` lines of the session pane's screen dump.
func (r *Runtime) GetOutput(ctx context.Context, handle ports.RuntimeHandle, lines int) (string, error) {
	id, paneID, err := handleID(handle)
	if err != nil {
		return "", err
	}
	if lines <= 0 {
		return "", errors.New("zellij runtime: lines must be positive")
	}
	out, err := r.run(ctx, dumpScreenArgs(id, paneID)...)
	if err != nil {
		return "", fmt.Errorf("zellij runtime: capture output %s/%s: %w", id, paneID, err)
	}
	return tailLines(trimTrailingBlankLines(string(out)), lines), nil
}

// IsAlive reports whether the handle's session still appears in `zellij
// list-sessions`. Only the documented "no sessions exist" failure counts as a
// definitive "not alive"; any other list-sessions failure is reported as a
// probe error so callers (the reaper feeding the LCM) treat it as a failed
// probe, never as proof of death.
func (r *Runtime) IsAlive(ctx context.Context, handle ports.RuntimeHandle) (bool, error) {
	id, _, err := handleID(handle)
	if err != nil {
		return false, err
	}
	out, err := r.run(ctx, listSessionsArgs()...)
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && noActiveSessionsOutput(string(out)) {
			return false, nil
		}
		return false, fmt.Errorf("zellij runtime: probe session %s: %w", id, err)
	}
	return sessionListedAlive(string(out), id), nil
}

// noActiveSessionsOutput reports whether a non-zero `zellij list-sessions`
// failed because no sessions exist at all — the one exit-error case that is a
// definitive "dead" rather than a probe failure. zellij 0.44 emits either
// "No active zellij sessions found." or "There is no active session!".
func noActiveSessionsOutput(out string) bool {
	s := strings.ToLower(out)
	return strings.Contains(s, "no active") && strings.Contains(s, "session")
}

func deleteSessionMissingOutput(out string) bool {
	s := strings.ToLower(out)
	if noActiveSessionsOutput(s) {
		return true
	}
	return strings.Contains(s, "session") &&
		(strings.Contains(s, "not found") ||
			strings.Contains(s, "does not exist") ||
			strings.Contains(s, "not exist") ||
			strings.Contains(s, "not a session"))
}

// AttachCommand returns the argv a human runs to attach their terminal to the
// session, plus an optional env block that the spawn should apply (used on
// Windows where wrapping the attach in an `env` shim is unsafe under ConPTY).
func (r *Runtime) AttachCommand(handle ports.RuntimeHandle) ([]string, []string, error) {
	id, _, err := handleID(handle)
	if err != nil {
		return nil, nil, err
	}
	args := append([]string{}, r.baseArgs()...)
	args = append(args, attachArgs(id)...)
	argv, env := attachCommandWithEnv(r.binary, r.socketDir, args...)
	return argv, env, nil
}

func (r *Runtime) ensureSupportedVersion(ctx context.Context) error {
	out, err := r.run(ctx, versionArgs()...)
	if err != nil {
		return fmt.Errorf("zellij runtime: check version: %w", err)
	}
	if _, err := CheckVersionOutput(string(out)); err != nil {
		return fmt.Errorf("zellij runtime: check version: %w", err)
	}
	return nil
}

func (r *Runtime) writeLayout(cfg ports.RuntimeConfig) (string, map[string]string, func(), error) {
	launchEnv := cfg.Env
	var cleanupLaunchSpec func()
	if runtime.GOOS == "windows" {
		specPath, err := agentlaunch.WriteTemp(agentlaunch.Spec{
			WorkspacePath: cfg.WorkspacePath,
			Argv:          cfg.Argv,
			FallbackArgv:  windowsFallbackShellArgv(r.shell),
		})
		if err != nil {
			return "", nil, nil, fmt.Errorf("zellij runtime: %w", err)
		}
		cleanupLaunchSpec = func() { _ = os.Remove(specPath) }
		cfg.Argv = windowsLaunchArgv(r.launcher)
		launchEnv = windowsLaunchEnv(cfg.Env, r.launcher, specPath)
	}

	file, err := os.CreateTemp(os.TempDir(), "ao-zellij-layout-*.kdl")
	if err != nil {
		if cleanupLaunchSpec != nil {
			cleanupLaunchSpec()
		}
		return "", nil, nil, fmt.Errorf("zellij runtime: create layout temp file: %w", err)
	}
	path := file.Name()
	if _, err := file.WriteString(buildLayout(cfg, r.shell)); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		if cleanupLaunchSpec != nil {
			cleanupLaunchSpec()
		}
		return "", nil, nil, fmt.Errorf("zellij runtime: write layout temp file: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		if cleanupLaunchSpec != nil {
			cleanupLaunchSpec()
		}
		return "", nil, nil, fmt.Errorf("zellij runtime: close layout temp file: %w", err)
	}
	return path, launchEnv, cleanupLaunchSpec, nil
}

// windowsLaunchEnv augments cfg.Env with the AO_LAUNCH_SPEC pointer the `ao
// launch` trampoline reads, and prepends the launcher's directory to PATH so
// the trampoline (i.e. `ao` itself) is resolvable in the spawned environment.
func windowsLaunchEnv(env map[string]string, launcherBinary, specPath string) map[string]string {
	launchEnv := make(map[string]string, len(env)+2)
	for k, v := range env {
		launchEnv[k] = v
	}
	launchEnv[agentlaunch.EnvSpecPath] = specPath
	if dir := launcherDir(launcherBinary); dir != "" {
		base := launchEnv["PATH"]
		if base == "" {
			base = getenv("PATH")
		}
		if base == "" {
			launchEnv["PATH"] = dir
		} else {
			launchEnv["PATH"] = dir + string(os.PathListSeparator) + base
		}
	}
	return launchEnv
}

func launcherDir(launcherBinary string) string {
	if launcherBinary == "" || !filepath.IsAbs(launcherBinary) {
		return ""
	}
	return filepath.Dir(launcherBinary)
}

func (r *Runtime) findAgentPane(ctx context.Context, id string) (string, error) {
	deadline := time.Now().Add(r.timeout)
	var lastErr error
	for {
		out, err := r.run(ctx, listPanesArgs(id)...)
		if err == nil {
			paneID, parseErr := agentPaneID(out)
			if parseErr == nil {
				return paneID, nil
			}
			lastErr = parseErr
		} else {
			lastErr = err
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("zellij runtime: list panes %s: %w", id, lastErr)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func (r *Runtime) waitForPaneReady(ctx context.Context, id, paneID string) error {
	if runtime.GOOS != "windows" {
		return nil
	}

	deadline := time.Now().Add(r.timeout)
	var lastErr error
	for {
		out, err := r.run(ctx, listPanesArgs(id)...)
		if err == nil {
			pane, parseErr := paneByID(out, paneID)
			if parseErr == nil {
				if pane.Exited {
					return fmt.Errorf("zellij runtime: pane %s/%s exited before ready", id, paneID)
				}
				if paneReady(pane) {
					return nil
				}
				lastErr = fmt.Errorf("pane %s/%s is not ready", id, paneID)
			} else {
				lastErr = parseErr
			}
		} else {
			lastErr = err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("zellij runtime: wait for pane %s/%s: %w", id, paneID, lastErr)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func (r *Runtime) run(ctx context.Context, args ...string) ([]byte, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	fullArgs := append(r.baseArgs(), args...)
	out, err := r.runner.Run(cmdCtx, r.env(), r.binary, fullArgs...)
	if cmdCtx.Err() != nil {
		return out, cmdCtx.Err()
	}
	if err != nil {
		return out, commandError{err: err, output: strings.TrimSpace(string(out))}
	}
	return out, nil
}

// startWithEnv fires zellij in the background with extra env vars merged onto
// the runtime's base env. Used by the Windows createSession path so the daemon
// is not blocked waiting on zellij's `--create-background` to settle.
func (r *Runtime) startWithEnv(extra map[string]string, args ...string) error {
	fullArgs := append(r.baseArgs(), args...)
	if err := r.runner.Start(r.envWith(extra), r.binary, fullArgs...); err != nil {
		return commandError{err: err}
	}
	return nil
}

func (r *Runtime) baseArgs() []string {
	args := []string{}
	if r.configDir != "" {
		args = append(args, "--config-dir", r.configDir)
	}
	return args
}

func (r *Runtime) env() []string {
	return r.envWith(nil)
}

func (r *Runtime) envWith(extra map[string]string) []string {
	env := zellijColorEnv(nil)
	if r.socketDir == "" {
		return appendRuntimeEnv(env, extra)
	}
	env = append(env, "ZELLIJ_SOCKET_DIR="+r.socketDir)
	return appendRuntimeEnv(env, extra)
}

func appendRuntimeEnv(env []string, extra map[string]string) []string {
	for _, key := range sortedKeys(extra) {
		env = append(env, key+"="+extra[key])
	}
	return env
}

func attachCommandWithEnv(binary, socketDir string, args ...string) ([]string, []string) {
	if runtime.GOOS == "windows" {
		// Windows ConPTY attaches the child directly. Avoid shell wrappers here:
		// malformed ConPTY startup around powershell.exe/cmd.exe surfaces as modal
		// application-error dialogs. Per-session ZELLIJ_SOCKET_DIR is delivered
		// via the spawn's env block (CreateProcess) instead of an `env` shim.
		var envBlock []string
		if socketDir != "" {
			envBlock = upsertEnv(append([]string(nil), os.Environ()...), "ZELLIJ_SOCKET_DIR="+socketDir)
		}
		return append([]string{binary}, args...), envBlock
	}
	env := zellijColorEnv(nil)
	if socketDir != "" {
		env = append(env, "ZELLIJ_SOCKET_DIR="+socketDir)
	}
	argv := []string{"env", "-u", "NO_COLOR"}
	argv = append(argv, env...)
	argv = append(argv, binary)
	return append(argv, args...), nil
}

func zellijCommandEnv(base, overrides []string) []string {
	env := zellijColorEnv(append([]string(nil), base...))
	for _, pair := range overrides {
		env = upsertEnv(env, pair)
	}
	return env
}

func zellijColorEnv(env []string) []string {
	if runtime.GOOS == "windows" {
		return env
	}
	env = removeEnv(env, "NO_COLOR")
	env = upsertEnv(env, "TERM="+defaultZellijTerm)
	env = upsertEnv(env, "COLORTERM="+defaultZellijColor)
	return env
}

func upsertEnv(env []string, pair string) []string {
	key, _, ok := strings.Cut(pair, "=")
	if !ok {
		return env
	}
	prefix := key + "="
	for i, current := range env {
		if strings.HasPrefix(current, prefix) {
			env[i] = pair
			return env
		}
	}
	return append(env, pair)
}

func removeEnv(env []string, key string) []string {
	prefix := key + "="
	out := env[:0]
	for _, current := range env {
		if strings.HasPrefix(current, prefix) {
			continue
		}
		out = append(out, current)
	}
	return out
}

func zellijSessionName(id domain.SessionID) (string, error) {
	raw := string(id)
	if raw == "" {
		return "", errors.New("zellij runtime: session id is required")
	}
	return SessionName(raw), nil
}

// SessionName returns the zellij session name the runtime registers for a given
// session id — applying the same sanitisation Create does. Callers that print an
// attach hint (e.g. `ao spawn`) must use this rather than the raw id, since a
// long or non-conforming id maps to a different, sanitised session name.
func SessionName(id string) string {
	if sessionIDPattern.MatchString(id) && len(id) <= 48 {
		return id
	}
	return sanitizedSessionName(id)
}

func sanitizedSessionName(raw string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range raw {
		valid := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-'
		if valid {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	base := strings.Trim(b.String(), "-")
	if base == "" {
		base = "session"
	}
	if len(base) > 32 {
		base = strings.TrimRight(base[:32], "-")
	}
	sum := sha256.Sum256([]byte(raw))
	return base + "-" + hex.EncodeToString(sum[:4])
}

func validateSessionID(id string) error {
	if id == "" {
		return errors.New("zellij runtime: session id is required")
	}
	if !sessionIDPattern.MatchString(id) {
		return fmt.Errorf("zellij runtime: invalid session id %q", id)
	}
	return nil
}

func validatePaneID(id string) error {
	if id == "" {
		return errors.New("zellij runtime: pane id is required")
	}
	if !paneIDPattern.MatchString(id) {
		return fmt.Errorf("zellij runtime: invalid pane id %q", id)
	}
	return nil
}

func handleID(handle ports.RuntimeHandle) (string, string, error) {
	parts := strings.Split(handle.ID, "/")
	if len(parts) == 1 {
		if err := validateSessionID(parts[0]); err != nil {
			return "", "", err
		}
		return parts[0], terminalPaneID(0), nil
	}
	if len(parts) != 2 {
		return "", "", fmt.Errorf("zellij runtime: invalid handle id %q", handle.ID)
	}
	if err := validateSessionID(parts[0]); err != nil {
		return "", "", err
	}
	if err := validatePaneID(parts[1]); err != nil {
		return "", "", err
	}
	return parts[0], parts[1], nil
}

type paneInfo struct {
	ID              int    `json:"id"`
	IsPlugin        bool   `json:"is_plugin"`
	Title           string `json:"title"`
	Exited          bool   `json:"exited"`
	TerminalCommand string `json:"terminal_command"`
	PaneCommand     string `json:"pane_command"`
}

func agentPaneID(out []byte) (string, error) {
	panes, err := parsePanes(out)
	if err != nil {
		return "", err
	}
	for _, pane := range panes {
		if !pane.IsPlugin && pane.Title == agentPaneName {
			return terminalPaneID(pane.ID), nil
		}
	}
	for _, pane := range panes {
		if !pane.IsPlugin {
			return terminalPaneID(pane.ID), nil
		}
	}
	return "", errors.New("agent pane not found")
}

func paneByID(out []byte, paneID string) (paneInfo, error) {
	panes, err := parsePanes(out)
	if err != nil {
		return paneInfo{}, err
	}
	for _, pane := range panes {
		if !pane.IsPlugin && terminalPaneID(pane.ID) == paneID {
			return pane, nil
		}
	}
	return paneInfo{}, fmt.Errorf("pane %s not found", paneID)
}

func parsePanes(out []byte) ([]paneInfo, error) {
	var panes []paneInfo
	if err := json.Unmarshal(out, &panes); err != nil {
		return nil, fmt.Errorf("parse panes: %w", err)
	}
	return panes, nil
}

func paneReady(pane paneInfo) bool {
	if pane.PaneCommand != "" {
		return true
	}
	return pane.TerminalCommand == ""
}

func chunks(s string, maxBytes int) []string {
	if s == "" {
		return []string{""}
	}
	if maxBytes <= 0 || len(s) <= maxBytes {
		return []string{s}
	}
	parts := []string{}
	for s != "" {
		if len(s) <= maxBytes {
			parts = append(parts, s)
			break
		}
		end := maxBytes
		for end > 0 && !utf8.ValidString(s[:end]) {
			end--
		}
		if end == 0 {
			_, size := utf8.DecodeRuneInString(s)
			end = size
		}
		parts = append(parts, s[:end])
		s = s[end:]
	}
	return parts
}

func tailLines(s string, n int) string {
	if n <= 0 || s == "" {
		return ""
	}
	lines := strings.SplitAfter(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "")
}

func trimTrailingBlankLines(s string) string {
	if s == "" {
		return ""
	}
	lines := strings.SplitAfter(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for len(lines) > 0 && strings.TrimRight(lines[len(lines)-1], "\r\n") == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "")
}

// RequiredVersion returns the minimum Zellij version AO's runtime adapter
// supports.
func RequiredVersion() string { return minSupportedVersion().String() }

// CheckVersionOutput parses `zellij --version` output, returning the parsed
// version when it satisfies AO's minimum runtime requirement.
func CheckVersionOutput(out string) (string, error) {
	version, err := parseVersion(out)
	if err != nil {
		return "", err
	}
	if compareVersion(version, minSupportedVersion()) < 0 {
		return version.String(), fmt.Errorf("unsupported zellij version %s; require >= %s", version, RequiredVersion())
	}
	return version.String(), nil
}

func minSupportedVersion() semver { return semver{minMajor, minMinor, minPatch} }

type semver struct {
	major int
	minor int
	patch int
}

func (v semver) String() string {
	return fmt.Sprintf("%d.%d.%d", v.major, v.minor, v.patch)
}

func parseVersion(out string) (semver, error) {
	fields := strings.Fields(strings.TrimSpace(out))
	if len(fields) == 0 {
		return semver{}, errors.New("empty version output")
	}
	raw := strings.TrimPrefix(fields[len(fields)-1], "v")
	parts := strings.Split(raw, ".")
	if len(parts) < 3 {
		return semver{}, fmt.Errorf("invalid version output %q", strings.TrimSpace(out))
	}
	major, err := parseVersionPart(parts[0])
	if err != nil {
		return semver{}, fmt.Errorf("invalid version output %q", strings.TrimSpace(out))
	}
	minor, err := parseVersionPart(parts[1])
	if err != nil {
		return semver{}, fmt.Errorf("invalid version output %q", strings.TrimSpace(out))
	}
	patch, err := parseVersionPart(parts[2])
	if err != nil {
		return semver{}, fmt.Errorf("invalid version output %q", strings.TrimSpace(out))
	}
	return semver{major: major, minor: minor, patch: patch}, nil
}

func parseVersionPart(s string) (int, error) {
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0, errors.New("missing version number")
	}
	return strconv.Atoi(s[:end])
}

func compareVersion(a, b semver) int {
	if a.major != b.major {
		return a.major - b.major
	}
	if a.minor != b.minor {
		return a.minor - b.minor
	}
	return a.patch - b.patch
}

func sessionListedAlive(out, id string) bool {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != id {
			continue
		}
		return !strings.Contains(line, "(EXITED")
	}
	return false
}

type commandError struct {
	err    error
	output string
}

func (e commandError) Error() string {
	if e.output == "" {
		return e.err.Error()
	}
	return e.err.Error() + ": " + e.output
}

func (e commandError) Unwrap() error { return e.err }
