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
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	defaultTimeout = 5 * time.Second
	minMajor       = 0
	minMinor       = 44
	minPatch       = 3
)

var sessionIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
var paneIDPattern = regexp.MustCompile(`^terminal_[0-9]+$`)

var getenv = os.Getenv

type Options struct {
	Binary    string
	Timeout   time.Duration
	Shell     string
	SocketDir string
	ConfigDir string
	ChunkSize int
}

type Runtime struct {
	binary    string
	timeout   time.Duration
	shell     string
	socketDir string
	configDir string
	chunkSize int
	runner    runner
}

var _ ports.Runtime = (*Runtime)(nil)

type runner interface {
	Run(ctx context.Context, env []string, name string, args ...string) ([]byte, error)
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	return cmd.CombinedOutput()
}

func New(opts Options) *Runtime {
	binary := opts.Binary
	if binary == "" {
		binary = "zellij"
	}
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
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
	return &Runtime{binary: binary, timeout: timeout, shell: shellPath, socketDir: opts.SocketDir, configDir: opts.ConfigDir, chunkSize: chunkSize, runner: execRunner{}}
}

func (r *Runtime) Create(ctx context.Context, cfg ports.RuntimeConfig) (ports.RuntimeHandle, error) {
	id, err := zellijSessionName(cfg.SessionID)
	if err != nil {
		return ports.RuntimeHandle{}, err
	}
	if cfg.WorkspacePath == "" {
		return ports.RuntimeHandle{}, errors.New("zellij runtime: workspace path is required")
	}
	if cfg.LaunchCommand == "" {
		return ports.RuntimeHandle{}, errors.New("zellij runtime: launch command is required")
	}
	if err := r.ensureSupportedVersion(ctx); err != nil {
		return ports.RuntimeHandle{}, err
	}

	layoutPath, err := r.writeLayout(cfg)
	if err != nil {
		return ports.RuntimeHandle{}, err
	}
	defer os.Remove(layoutPath)

	if _, err := r.run(ctx, createSessionArgs(id, layoutPath)...); err != nil {
		return ports.RuntimeHandle{}, fmt.Errorf("zellij runtime: create session %s: %w", id, err)
	}
	paneID, err := r.findAgentPane(ctx, id)
	if err != nil {
		_ = r.Destroy(context.Background(), ports.RuntimeHandle{ID: id, RuntimeName: runtimeName})
		return ports.RuntimeHandle{}, err
	}
	return ports.RuntimeHandle{ID: handleIDValue(id, paneID), RuntimeName: runtimeName}, nil
}

func (r *Runtime) Destroy(ctx context.Context, handle ports.RuntimeHandle) error {
	id, _, err := handleID(handle)
	if err != nil {
		return err
	}
	if _, err := r.run(ctx, killSessionArgs(id)...); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil
		}
		return fmt.Errorf("zellij runtime: destroy session %s: %w", id, err)
	}
	return nil
}

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
	return tailLines(string(out), lines), nil
}

func (r *Runtime) IsAlive(ctx context.Context, handle ports.RuntimeHandle) (bool, error) {
	id, _, err := handleID(handle)
	if err != nil {
		return false, err
	}
	out, err := r.run(ctx, listSessionsArgs()...)
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return false, nil
		}
		return false, fmt.Errorf("zellij runtime: probe session %s: %w", id, err)
	}
	return sessionListedAlive(string(out), id), nil
}

func (r *Runtime) AttachCommand(handle ports.RuntimeHandle) ([]string, error) {
	id, _, err := handleID(handle)
	if err != nil {
		return nil, err
	}
	args := append([]string{}, r.baseArgs()...)
	args = append(args, attachArgs(id)...)
	if r.socketDir == "" {
		return append([]string{r.binary}, args...), nil
	}
	return attachCommandWithEnv(r.binary, r.socketDir, args...), nil
}

func (r *Runtime) ensureSupportedVersion(ctx context.Context) error {
	out, err := r.run(ctx, versionArgs()...)
	if err != nil {
		return fmt.Errorf("zellij runtime: check version: %w", err)
	}
	version, err := parseVersion(string(out))
	if err != nil {
		return fmt.Errorf("zellij runtime: check version: %w", err)
	}
	if compareVersion(version, semver{minMajor, minMinor, minPatch}) < 0 {
		return fmt.Errorf("zellij runtime: unsupported zellij version %s; require >= %d.%d.%d", version, minMajor, minMinor, minPatch)
	}
	return nil
}

func (r *Runtime) writeLayout(cfg ports.RuntimeConfig) (string, error) {
	file, err := os.CreateTemp(os.TempDir(), "ao-zellij-layout-*.kdl")
	if err != nil {
		return "", fmt.Errorf("zellij runtime: create layout temp file: %w", err)
	}
	path := file.Name()
	if _, err := file.WriteString(buildLayout(cfg, r.shell)); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("zellij runtime: write layout temp file: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("zellij runtime: close layout temp file: %w", err)
	}
	return path, nil
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

func (r *Runtime) baseArgs() []string {
	args := []string{}
	if r.configDir != "" {
		args = append(args, "--config-dir", r.configDir)
	}
	return args
}

func (r *Runtime) env() []string {
	if r.socketDir == "" {
		return nil
	}
	return []string{"ZELLIJ_SOCKET_DIR=" + r.socketDir}
}

func attachCommandWithEnv(binary, socketDir string, args ...string) []string {
	if socketDir == "" {
		return append([]string{binary}, args...)
	}
	if runtime.GOOS == "windows" {
		command := strings.Builder{}
		command.WriteString("$env:ZELLIJ_SOCKET_DIR = ")
		command.WriteString(psQuote(socketDir))
		command.WriteString("; & ")
		command.WriteString(psQuote(binary))
		for _, arg := range args {
			command.WriteByte(' ')
			command.WriteString(psQuote(arg))
		}
		return []string{"powershell.exe", "-NoLogo", "-NoProfile", "-Command", command.String()}
	}
	return append([]string{"env", "ZELLIJ_SOCKET_DIR=" + socketDir, binary}, args...)
}

func zellijSessionName(id domain.SessionID) (string, error) {
	raw := string(id)
	if raw == "" {
		return "", errors.New("zellij runtime: session id is required")
	}
	if sessionIDPattern.MatchString(raw) && len(raw) <= 48 {
		return raw, nil
	}
	return sanitizedSessionName(raw), nil
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
	if handle.RuntimeName != "" && handle.RuntimeName != runtimeName {
		return "", "", fmt.Errorf("zellij runtime: wrong runtime %q", handle.RuntimeName)
	}
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
	ID       int    `json:"id"`
	IsPlugin bool   `json:"is_plugin"`
	Title    string `json:"title"`
}

func agentPaneID(out []byte) (string, error) {
	var panes []paneInfo
	if err := json.Unmarshal(out, &panes); err != nil {
		return "", fmt.Errorf("parse panes: %w", err)
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

func chunks(s string, maxBytes int) []string {
	if s == "" {
		return []string{""}
	}
	if maxBytes <= 0 || len(s) <= maxBytes {
		return []string{s}
	}
	parts := []string{}
	for len(s) > 0 {
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
