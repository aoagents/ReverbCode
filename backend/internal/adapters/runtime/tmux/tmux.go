// Package tmux implements ports.Runtime using tmux sessions.
package tmux

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const defaultTimeout = 5 * time.Second
const longMessageThreshold = 512

var sessionIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

var getenv = os.Getenv

type Options struct {
	Binary  string
	Timeout time.Duration
	Shell   string
}

type Runtime struct {
	binary  string
	timeout time.Duration
	shell   string
	runner  runner
}

var _ ports.Runtime = (*Runtime)(nil)

type runner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

func New(opts Options) *Runtime {
	binary := opts.Binary
	if binary == "" {
		binary = "tmux"
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
		shellPath = "/bin/sh"
	}
	return &Runtime{binary: binary, timeout: timeout, shell: shellPath, runner: execRunner{}}
}

func (r *Runtime) Create(ctx context.Context, cfg ports.RuntimeConfig) (ports.RuntimeHandle, error) {
	id, err := tmuxSessionName(cfg.SessionID)
	if err != nil {
		return ports.RuntimeHandle{}, err
	}
	if cfg.WorkspacePath == "" {
		return ports.RuntimeHandle{}, errors.New("tmux runtime: workspace path is required")
	}
	if cfg.LaunchCommand == "" {
		return ports.RuntimeHandle{}, errors.New("tmux runtime: launch command is required")
	}

	script := wrapLaunchCommand(cfg, r.shell)
	if _, err := r.run(ctx, newSessionArgs(id, cfg.WorkspacePath, r.shell, script)...); err != nil {
		return ports.RuntimeHandle{}, fmt.Errorf("tmux runtime: create session %s: %w", id, err)
	}
	if _, err := r.run(ctx, setStatusOffArgs(id)...); err != nil {
		_ = r.Destroy(context.Background(), ports.RuntimeHandle{ID: id, RuntimeName: runtimeName})
		return ports.RuntimeHandle{}, fmt.Errorf("tmux runtime: disable status %s: %w", id, err)
	}
	return ports.RuntimeHandle{ID: id, RuntimeName: runtimeName}, nil
}

func (r *Runtime) Destroy(ctx context.Context, handle ports.RuntimeHandle) error {
	id, err := handleID(handle)
	if err != nil {
		return err
	}
	if _, err := r.run(ctx, killSessionArgs(id)...); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil
		}
		return fmt.Errorf("tmux runtime: destroy session %s: %w", id, err)
	}
	return nil
}

func (r *Runtime) SendMessage(ctx context.Context, handle ports.RuntimeHandle, message string) error {
	id, err := handleID(handle)
	if err != nil {
		return err
	}
	if useBuffer(message) {
		return r.sendViaBuffer(ctx, id, message)
	}
	if _, err := r.run(ctx, sendLiteralArgs(id, message)...); err != nil {
		return fmt.Errorf("tmux runtime: send message %s: %w", id, err)
	}
	if _, err := r.run(ctx, sendEnterArgs(id)...); err != nil {
		return fmt.Errorf("tmux runtime: send enter %s: %w", id, err)
	}
	return nil
}

func (r *Runtime) GetOutput(ctx context.Context, handle ports.RuntimeHandle, lines int) (string, error) {
	id, err := handleID(handle)
	if err != nil {
		return "", err
	}
	if lines <= 0 {
		return "", errors.New("tmux runtime: lines must be positive")
	}
	out, err := r.run(ctx, capturePaneArgs(id, lines)...)
	if err != nil {
		return "", fmt.Errorf("tmux runtime: capture output %s: %w", id, err)
	}
	return string(out), nil
}

func (r *Runtime) IsAlive(ctx context.Context, handle ports.RuntimeHandle) (bool, error) {
	id, err := handleID(handle)
	if err != nil {
		return false, err
	}
	_, err = r.run(ctx, hasSessionArgs(id)...)
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return false, nil
	}
	return false, fmt.Errorf("tmux runtime: probe session %s: %w", id, err)
}

func (r *Runtime) AttachCommand(handle ports.RuntimeHandle) ([]string, error) {
	id, err := handleID(handle)
	if err != nil {
		return nil, err
	}
	return append([]string{r.binary}, "attach", "-t", exactSessionTarget(id)), nil
}

func (r *Runtime) sendViaBuffer(ctx context.Context, id, message string) error {
	dir := os.TempDir()
	file, err := os.CreateTemp(dir, "ao-tmux-message-*")
	if err != nil {
		return fmt.Errorf("tmux runtime: create message temp file: %w", err)
	}
	path := file.Name()
	defer os.Remove(path)
	if _, err := file.WriteString(message); err != nil {
		_ = file.Close()
		return fmt.Errorf("tmux runtime: write message temp file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("tmux runtime: close message temp file: %w", err)
	}

	bufferName := "ao-" + filepath.Base(path)
	if _, err := r.run(ctx, loadBufferArgs(bufferName, path)...); err != nil {
		return fmt.Errorf("tmux runtime: load buffer %s: %w", id, err)
	}
	if _, err := r.run(ctx, pasteBufferArgs(id, bufferName)...); err != nil {
		return fmt.Errorf("tmux runtime: paste buffer %s: %w", id, err)
	}
	if _, err := r.run(ctx, sendEnterArgs(id)...); err != nil {
		return fmt.Errorf("tmux runtime: send enter %s: %w", id, err)
	}
	return nil
}

func (r *Runtime) run(ctx context.Context, args ...string) ([]byte, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	out, err := r.runner.Run(cmdCtx, r.binary, args...)
	if cmdCtx.Err() != nil {
		return out, cmdCtx.Err()
	}
	if err != nil {
		return out, commandError{err: err, output: strings.TrimSpace(string(out))}
	}
	return out, nil
}

func tmuxSessionName(id domain.SessionID) (string, error) {
	raw := string(id)
	if raw == "" {
		return "", errors.New("tmux runtime: session id is required")
	}
	if sessionIDPattern.MatchString(raw) {
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
	if len(base) > 40 {
		base = strings.TrimRight(base[:40], "-")
	}
	sum := sha256.Sum256([]byte(raw))
	return base + "-" + hex.EncodeToString(sum[:4])
}

func validateSessionID(id string) error {
	if id == "" {
		return errors.New("tmux runtime: session id is required")
	}
	if !sessionIDPattern.MatchString(id) {
		return fmt.Errorf("tmux runtime: invalid session id %q", id)
	}
	return nil
}

func handleID(handle ports.RuntimeHandle) (string, error) {
	if handle.RuntimeName != "" && handle.RuntimeName != runtimeName {
		return "", fmt.Errorf("tmux runtime: wrong runtime %q", handle.RuntimeName)
	}
	if err := validateSessionID(handle.ID); err != nil {
		return "", err
	}
	return handle.ID, nil
}

func useBuffer(message string) bool {
	return strings.Contains(message, "\n") || len(message) > longMessageThreshold
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
