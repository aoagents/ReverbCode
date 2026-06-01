package codex

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent"
)

func TestGetLaunchCommandBuildsCrossPlatformArgv(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "codex"}

	cmd, err := plugin.GetLaunchCommand(context.Background(), agent.LaunchConfig{
		Permissions:      agent.PermissionModeBypassPermissions,
		Prompt:           "-fix this",
		SystemPromptFile: filepath.Join("tmp", "prompt with spaces.md"),
		SystemPrompt:     "ignored",
	})
	if err != nil {
		t.Fatal(err)
	}

	want := []string{
		"codex",
		"-c", "check_for_update_on_startup=false",
		"--dangerously-bypass-approvals-and-sandbox",
		"-c", "model_instructions_file=" + filepath.Join("tmp", "prompt with spaces.md"),
		"--", "-fix this",
	}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("unexpected command\nwant: %#v\n got: %#v", want, cmd)
	}
}

func TestGetLaunchCommandMapsApprovalModes(t *testing.T) {
	tests := []struct {
		name        string
		permission  agent.PermissionMode
		want        []string
		notExpected string
	}{
		{
			name:        "default",
			permission:  agent.PermissionModeDefault,
			notExpected: "--ask-for-approval",
		},
		{
			name:       "accept-edits",
			permission: agent.PermissionModeAcceptEdits,
			want:       []string{"--ask-for-approval", "on-request"},
		},
		{
			name:       "auto",
			permission: agent.PermissionModeAuto,
			want:       []string{"--ask-for-approval", "on-request", "-c", `approvals_reviewer="auto_review"`},
		},
		{
			name:       "bypass-permissions",
			permission: agent.PermissionModeBypassPermissions,
			want:       []string{"--dangerously-bypass-approvals-and-sandbox"},
		},
		{
			name:        "empty",
			permission:  "",
			notExpected: "--ask-for-approval",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plugin := &Plugin{resolvedBinary: "codex"}
			cmd, err := plugin.GetLaunchCommand(context.Background(), agent.LaunchConfig{
				Permissions: tt.permission,
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(tt.want) > 0 && !containsSubsequence(cmd, tt.want) {
				t.Fatalf("command %#v does not contain %#v", cmd, tt.want)
			}
			if tt.notExpected != "" && contains(cmd, tt.notExpected) {
				t.Fatalf("command %#v contains %q", cmd, tt.notExpected)
			}
		})
	}
}

func TestGetPromptDeliveryStrategyIsInCommand(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "codex"}

	got, err := plugin.GetPromptDeliveryStrategy(context.Background(), agent.LaunchConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if got != agent.PromptDeliveryInCommand {
		t.Fatalf("unexpected strategy: %q", got)
	}
}

func TestGetConfigSpecHasNoCustomFieldsYet(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "codex"}

	spec, err := plugin.GetConfigSpec(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(spec.Fields) != 0 {
		t.Fatalf("unexpected config fields: %#v", spec.Fields)
	}
}

func TestGetAgentHooksInstallsCodexHooks(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "codex"}
	workspace := t.TempDir()
	hooksDir := filepath.Join(workspace, ".codex")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	hooksPath := filepath.Join(hooksDir, "hooks.json")
	existing := `{"hooks":{"Stop":[{"matcher":null,"hooks":[{"type":"command","command":"custom stop hook","timeout":3}]}]}}`
	if err := os.WriteFile(hooksPath, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := agent.WorkspaceHookConfig{
		DataDir:       t.TempDir(),
		SessionID:     "sess-1",
		WorkspacePath: workspace,
	}
	if err := plugin.GetAgentHooks(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	// A second install must not duplicate Better-AO hook commands.
	if err := plugin.GetAgentHooks(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatal(err)
	}
	var config codexHookFile
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	if config.Hooks == nil {
		t.Fatalf("hooks config missing hooks object: %#v", config)
	}
	templateHooks, err := codexEmbeddedHookGroups()
	if err != nil {
		t.Fatal(err)
	}
	for event, templateGroups := range templateHooks {
		entries := config.Hooks[event]
		for _, templateGroup := range templateGroups {
			for _, hook := range templateGroup.Hooks {
				count := countCodexHookCommand(entries, hook.Command)
				if count != 1 {
					t.Fatalf("%s command count = %d, want 1 in %#v", event, count, entries)
				}
			}
		}
	}
	stopEntries := config.Hooks["Stop"]
	if countCodexHookCommand(stopEntries, "custom stop hook") != 1 {
		t.Fatalf("existing Stop hook was not preserved: %#v", stopEntries)
	}

	configData, err := os.ReadFile(filepath.Join(workspace, ".codex", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(configData), codexHooksFeatureLine) {
		t.Fatalf("config.toml missing hooks feature flag: %s", configData)
	}
}

func TestGetRestoreCommandReadsAgentSessionID(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "codex"}

	cmd, ok, err := plugin.GetRestoreCommand(context.Background(), agent.RestoreConfig{
		Permissions: agent.PermissionModeAuto,
		Session: agent.SessionRef{
			Metadata: map[string]string{codexAgentSessionIDMetadataKey: "thread-123"},
		},
	})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if !ok {
		t.Fatal("ok = false, want true")
	}
	want := []string{
		"codex",
		"resume",
		"-c", "check_for_update_on_startup=false",
		"--ask-for-approval", "on-request",
		"-c", `approvals_reviewer="auto_review"`,
		"thread-123",
	}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("restore cmd\nwant: %#v\n got: %#v", want, cmd)
	}
}

func TestGetRestoreCommandFalseWithoutAgentSessionID(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "codex"}

	cases := []struct {
		name string
		ref  agent.SessionRef
	}{
		{"empty session ref", agent.SessionRef{}},
		{"empty metadata", agent.SessionRef{Metadata: map[string]string{}}},
		{"blank agent session metadata", agent.SessionRef{Metadata: map[string]string{codexAgentSessionIDMetadataKey: "   "}}},
		{"workspace path only", agent.SessionRef{WorkspacePath: "/some/path"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd, ok, err := plugin.GetRestoreCommand(context.Background(), agent.RestoreConfig{
				Permissions: agent.PermissionModeAuto,
				Session:     tc.ref,
			})
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if ok {
				t.Fatalf("ok = true, want false")
			}
			if cmd != nil {
				t.Fatalf("cmd = %#v, want nil", cmd)
			}
		})
	}
}

func TestSessionInfoReadsHookMetadata(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "codex"}

	info, ok, err := plugin.SessionInfo(context.Background(), agent.SessionRef{
		WorkspacePath: "/some/path",
		Metadata: map[string]string{
			codexAgentSessionIDMetadataKey: "thread-123",
			codexTitleMetadataKey:          "Fix login redirect",
			codexSummaryMetadataKey:        "Updated the auth callback and tests.",
			"ignored":                      "not returned",
		},
	})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if !ok {
		t.Fatalf("ok = false, want true")
	}
	if info.AgentSessionID != "thread-123" {
		t.Fatalf("AgentSessionID = %q, want native id", info.AgentSessionID)
	}
	if info.Title != "Fix login redirect" {
		t.Fatalf("Title = %q, want hook title", info.Title)
	}
	if info.Summary != "Updated the auth callback and tests." {
		t.Fatalf("Summary = %q, want hook summary", info.Summary)
	}
	if info.Metadata != nil {
		t.Fatalf("Metadata = %#v, want nil for Codex", info.Metadata)
	}
}

func TestSessionInfoFalseWhenNoHookMetadata(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "codex"}

	info, ok, err := plugin.SessionInfo(context.Background(), agent.SessionRef{
		WorkspacePath: "/some/path",
		Metadata:      map[string]string{},
	})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if ok {
		t.Fatalf("ok = true, want false")
	}
	if !reflect.DeepEqual(info, agent.SessionInfo{}) {
		t.Fatalf("info = %#v, want zero value", info)
	}
}

func contains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func containsSubsequence(values []string, needle []string) bool {
	if len(needle) == 0 {
		return true
	}

	for start := range values {
		if start+len(needle) > len(values) {
			return false
		}
		ok := true
		for offset, want := range needle {
			if values[start+offset] != want {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}

	return false
}

func countCodexHookCommand(entries []codexMatcherGroup, command string) int {
	count := 0
	for _, entry := range entries {
		for _, hook := range entry.Hooks {
			if hook.Command == command {
				count++
			}
		}
	}
	return count
}
