// Package portshim bridges the richer adapters/agent.Agent interface onto the
// narrower ports.Agent the Session Manager consumes. The richer interface
// returns argv slices and takes a context; ports.Agent returns a single shell
// string and is context-free. The shim joins argv with POSIX shell quoting so
// the zellij runtime, which evaluates LaunchCommand under `sh -lc`, sees the
// agent's argv intact.
package portshim

import (
	"context"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Shim wraps an adapters/agent.Agent and satisfies ports.Agent. The shim is
// context-free at its API surface; it threads context.Background() into the
// richer interface. That matches the existing ports.Agent shape — extending it
// is a separate change.
type Shim struct {
	agent agent.Agent
}

// New constructs a Shim. agent is required; nil is not supported.
func New(a agent.Agent) *Shim { return &Shim{agent: a} }

var _ ports.Agent = (*Shim)(nil)

// GetLaunchCommand asks the wrapped agent for its launch argv and renders it as
// a single POSIX-shell-safe string. An adapter error or empty argv yields "".
func (s *Shim) GetLaunchCommand(cfg ports.AgentConfig) string {
	argv, err := s.agent.GetLaunchCommand(context.Background(), launchConfigFor(cfg))
	if err != nil {
		return ""
	}
	return joinShellArgv(argv)
}

// GetEnvironment returns nil: the richer agent interface doesn't carry the env
// keys ports.AgentConfig exposes, and the SM layers AO_SESSION_ID,
// AO_PROJECT_ID, AO_ISSUE_ID on top of whatever the agent contributes. A nil
// map is fine here — session.spawnEnv treats nil as empty.
func (s *Shim) GetEnvironment(ports.AgentConfig) map[string]string {
	return nil
}

// GetRestoreCommand resumes a native agent session given its agentSessionID and
// returns the resume command as a POSIX-shell-safe string. An adapter error or
// ok=false yields "" — the SM falls back to a fresh Spawn.
func (s *Shim) GetRestoreCommand(agentSessionID string) string {
	cfg := agent.RestoreConfig{
		Session: agent.SessionRef{
			ID: agentSessionID,
			Metadata: map[string]string{
				agent.MetadataKeyAgentSessionID: agentSessionID,
			},
		},
	}
	argv, ok, err := s.agent.GetRestoreCommand(context.Background(), cfg)
	if err != nil || !ok {
		return ""
	}
	return joinShellArgv(argv)
}

func launchConfigFor(cfg ports.AgentConfig) agent.LaunchConfig {
	return agent.LaunchConfig{
		SessionID:     string(cfg.SessionID),
		WorkspacePath: cfg.WorkspacePath,
		Prompt:        cfg.Prompt,
	}
}

// joinShellArgv renders argv as a single string the POSIX shell will re-parse
// into the same tokens. Each arg is quoted in single quotes unless it consists
// only of characters guaranteed safe to leave bare.
func joinShellArgv(argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	parts := make([]string, len(argv))
	for i, a := range argv {
		parts[i] = shellQuote(a)
	}
	return strings.Join(parts, " ")
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if isShellSafe(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// isShellSafe matches the conservative bash-completion convention: letters,
// digits, and a handful of punctuation that never trigger expansion or word
// splitting. Anything else is quoted.
func isShellSafe(s string) bool {
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-', r == '_', r == '/', r == '.', r == ',', r == ':', r == '+', r == '@', r == '=':
			continue
		}
		return false
	}
	return true
}
