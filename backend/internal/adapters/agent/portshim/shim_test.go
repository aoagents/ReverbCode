package portshim_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/portshim"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/session"
)

type fakeAgent struct {
	launchCmd     []string
	launchErr     error
	restoreCmd    []string
	restoreOK     bool
	restoreErr    error
	gotLaunchCfg  agent.LaunchConfig
	gotRestoreCfg agent.RestoreConfig
}

func (f *fakeAgent) GetConfigSpec(context.Context) (agent.ConfigSpec, error) {
	return agent.ConfigSpec{}, nil
}
func (f *fakeAgent) GetLaunchCommand(_ context.Context, cfg agent.LaunchConfig) ([]string, error) {
	f.gotLaunchCfg = cfg
	return f.launchCmd, f.launchErr
}
func (f *fakeAgent) GetPromptDeliveryStrategy(context.Context, agent.LaunchConfig) (agent.PromptDeliveryStrategy, error) {
	return agent.PromptDeliveryInCommand, nil
}
func (f *fakeAgent) GetAgentHooks(context.Context, agent.WorkspaceHookConfig) error { return nil }
func (f *fakeAgent) GetRestoreCommand(_ context.Context, cfg agent.RestoreConfig) ([]string, bool, error) {
	f.gotRestoreCfg = cfg
	return f.restoreCmd, f.restoreOK, f.restoreErr
}
func (f *fakeAgent) SessionInfo(context.Context, agent.SessionRef) (agent.SessionInfo, bool, error) {
	return agent.SessionInfo{}, false, nil
}

func TestSatisfiesPortsAgent(t *testing.T) {
	var _ ports.Agent = (*portshim.Shim)(nil)
}

func TestGetLaunchCommand_JoinsArgvShellSafely(t *testing.T) {
	tests := []struct {
		name string
		argv []string
		want string
	}{
		{"simple", []string{"claude"}, "claude"},
		{"flags and prompt", []string{"claude", "--", "do it"}, "claude -- 'do it'"},
		{"path with spaces", []string{"/Applications/My App/claude", "--flag"}, "'/Applications/My App/claude' --flag"},
		{"prompt with single quote", []string{"claude", "--", "it's fine"}, `claude -- 'it'\''s fine'`},
		{"empty argv", []string{}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := portshim.New(&fakeAgent{launchCmd: tc.argv})
			got := s.GetLaunchCommand(ports.AgentConfig{})
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestGetLaunchCommand_PropagatesAgentConfig(t *testing.T) {
	fake := &fakeAgent{launchCmd: []string{"claude"}}
	s := portshim.New(fake)
	cfg := ports.AgentConfig{SessionID: "p-1", WorkspacePath: "/ws/p-1", Prompt: "hello"}
	_ = s.GetLaunchCommand(cfg)
	if fake.gotLaunchCfg.SessionID != "p-1" {
		t.Errorf("SessionID not propagated: %+v", fake.gotLaunchCfg)
	}
	if fake.gotLaunchCfg.WorkspacePath != "/ws/p-1" {
		t.Errorf("WorkspacePath not propagated: %+v", fake.gotLaunchCfg)
	}
	if fake.gotLaunchCfg.Prompt != "hello" {
		t.Errorf("Prompt not propagated: %+v", fake.gotLaunchCfg)
	}
}

func TestGetLaunchCommand_AgentErrorReturnsEmpty(t *testing.T) {
	fake := &fakeAgent{launchErr: errors.New("boom")}
	s := portshim.New(fake)
	got := s.GetLaunchCommand(ports.AgentConfig{SessionID: "p-1"})
	if got != "" {
		t.Fatalf("expected empty on error, got %q", got)
	}
}

func TestGetEnvironment_ReturnsAgentEnvKeysOnly(t *testing.T) {
	// The richer Agent interface doesn't carry the env keys the SM port supplies,
	// so the shim has nothing agent-specific to surface. SM layers AO_* on top.
	s := portshim.New(&fakeAgent{})
	got := s.GetEnvironment(ports.AgentConfig{SessionID: "p-1"})
	if len(got) != 0 {
		t.Fatalf("expected empty env from shim, got %v", got)
	}
	for _, k := range []string{session.EnvSessionID, session.EnvProjectID, session.EnvIssueID} {
		if _, ok := got[k]; ok {
			t.Errorf("shim must not pre-populate AO env key %s; SM owns it", k)
		}
	}
}

func TestGetRestoreCommand_JoinsWhenOK(t *testing.T) {
	fake := &fakeAgent{restoreCmd: []string{"claude", "--resume", "abc 123"}, restoreOK: true}
	s := portshim.New(fake)
	got := s.GetRestoreCommand("abc 123")
	want := `claude --resume 'abc 123'`
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if fake.gotRestoreCfg.Session.ID != "abc 123" {
		t.Errorf("session id not propagated: %+v", fake.gotRestoreCfg)
	}
}

func TestGetRestoreCommand_NotOKReturnsEmpty(t *testing.T) {
	fake := &fakeAgent{restoreOK: false}
	s := portshim.New(fake)
	if got := s.GetRestoreCommand("anything"); got != "" {
		t.Fatalf("expected empty when not restorable, got %q", got)
	}
}

func TestGetRestoreCommand_ErrorReturnsEmpty(t *testing.T) {
	fake := &fakeAgent{restoreErr: errors.New("boom")}
	s := portshim.New(fake)
	if got := s.GetRestoreCommand("x"); got != "" {
		t.Fatalf("expected empty on restore error, got %q", got)
	}
}

func TestGetRestoreCommand_PassesAgentSessionIDAsMetadata(t *testing.T) {
	// Claude-code (and Codex) read the native session id off cfg.Session.Metadata
	// ["agentSessionId"] to rebuild the --resume command. Pass it via both Session.ID
	// (the legacy fallback) and Session.Metadata so the richer adapter can find it.
	fake := &fakeAgent{restoreCmd: []string{"claude", "--resume", "x"}, restoreOK: true}
	s := portshim.New(fake)
	_ = s.GetRestoreCommand("native-uuid")
	gotID := fake.gotRestoreCfg.Session.ID
	if gotID != "native-uuid" {
		t.Errorf("Session.ID want native-uuid, got %q", gotID)
	}
	if m := fake.gotRestoreCfg.Session.Metadata[agent.MetadataKeyAgentSessionID]; m != "native-uuid" {
		t.Errorf("Session.Metadata[%s] want native-uuid, got %q", agent.MetadataKeyAgentSessionID, m)
	}
}

func TestShellQuotingDoesNotDoubleQuoteSafeStrings(t *testing.T) {
	// Safe identifiers (letters, digits, dash, dot, slash, underscore) should
	// pass through unquoted; quoting them would inflate every command.
	s := portshim.New(&fakeAgent{launchCmd: []string{"/usr/local/bin/claude", "--session-id", "abc-123_xyz.uuid"}})
	got := s.GetLaunchCommand(ports.AgentConfig{})
	if strings.Contains(got, "'") {
		t.Fatalf("got unexpected quotes: %q", got)
	}
}
