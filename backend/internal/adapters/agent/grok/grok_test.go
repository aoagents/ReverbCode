package grok

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestManifest(t *testing.T) {
	m := (&Plugin{}).Manifest()
	if m.ID != "grok" {
		t.Fatalf("ID = %q, want grok", m.ID)
	}
	if m.Name != "Grok Build" {
		t.Fatalf("Name = %q", m.Name)
	}
	hasAgent := false
	for _, c := range m.Capabilities {
		if c == adapters.CapabilityAgent {
			hasAgent = true
		}
	}
	if !hasAgent {
		t.Fatal("missing CapabilityAgent")
	}
}

func TestGetConfigSpecEmpty(t *testing.T) {
	spec, err := (&Plugin{}).GetConfigSpec(context.Background())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(spec.Fields) != 0 {
		t.Fatalf("expected no fields, got %d", len(spec.Fields))
	}
}

func TestGetPromptDeliveryStrategy(t *testing.T) {
	s, err := (&Plugin{}).GetPromptDeliveryStrategy(context.Background(), ports.LaunchConfig{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if s != ports.PromptDeliveryInCommand {
		t.Fatalf("strategy = %q, want in_command", s)
	}
}

func TestGetLaunchCommand(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "grok"}
	cmd, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		Prompt:      "do the thing",
		Permissions: ports.PermissionModeBypassPermissions,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	wantPrefix := []string{"grok", "--no-auto-update", "--permission-mode", "bypassPermissions", "-p", "do the thing"}
	if !reflect.DeepEqual(cmd, wantPrefix) {
		t.Fatalf("cmd = %#v, want prefix %#v", cmd, wantPrefix)
	}
}

func TestGetLaunchCommandDefaultPerms(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "grok"}
	cmd, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		Prompt: "fix it",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(cmd) < 4 || cmd[0] != "grok" || cmd[1] != "--no-auto-update" || cmd[2] != "-p" {
		t.Fatalf("cmd = %#v, want grok --no-auto-update -p ...", cmd)
	}
	if strings.Contains(strings.Join(cmd, " "), "permission-mode") {
		t.Fatal("should not have --permission-mode for default perms")
	}
}

func TestGetLaunchCommandAcceptEdits(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "grok"}
	cmd, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{
		Prompt:      "refactor auth",
		Permissions: ports.PermissionModeAcceptEdits,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	want := []string{"grok", "--no-auto-update", "--permission-mode", "acceptEdits", "-p", "refactor auth"}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("cmd = %#v, want %#v", cmd, want)
	}
}

func TestGetRestoreCommand(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "grok"}
	cmd, ok, err := plugin.GetRestoreCommand(context.Background(), ports.RestoreConfig{
		Session: ports.SessionRef{
			Metadata: map[string]string{
				ports.MetadataKeyAgentSessionID: "sess-abc123",
			},
		},
		Permissions: ports.PermissionModeBypassPermissions,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !ok {
		t.Fatal("ok=false, want true")
	}
	want := []string{"grok", "--no-auto-update", "--permission-mode", "bypassPermissions", "-r", "sess-abc123"}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("cmd = %#v, want %#v", cmd, want)
	}
}

func TestGetRestoreCommandNoID(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "grok"}
	_, ok, err := plugin.GetRestoreCommand(context.Background(), ports.RestoreConfig{
		Session: ports.SessionRef{Metadata: map[string]string{}},
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if ok {
		t.Fatal("ok=true with no agentSessionId, want false")
	}
}

func TestSessionInfoReadsHookMetadata(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "grok"}
	info, ok, err := plugin.SessionInfo(context.Background(), ports.SessionRef{
		Metadata: map[string]string{
			ports.MetadataKeyAgentSessionID: "grok-ses-1",
			ports.MetadataKeyTitle:          "Fix login redirect",
			ports.MetadataKeySummary:        "Updated the auth callback and tests.",
		},
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !ok {
		t.Fatal("ok=false, want true")
	}
	if info.AgentSessionID != "grok-ses-1" {
		t.Fatalf("AgentSessionID = %q, want grok-ses-1", info.AgentSessionID)
	}
	if info.Title != "Fix login redirect" {
		t.Fatalf("Title = %q", info.Title)
	}
	if info.Summary != "Updated the auth callback and tests." {
		t.Fatalf("Summary = %q", info.Summary)
	}
}

func TestSessionInfoFalseWhenNoHookMetadata(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "grok"}
	info, ok, err := plugin.SessionInfo(context.Background(), ports.SessionRef{
		Metadata: map[string]string{},
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if ok {
		t.Fatalf("ok=true with empty metadata, want false")
	}
	if !reflect.DeepEqual(info, ports.SessionInfo{}) {
		t.Fatalf("info = %#v, want zero", info)
	}
}

func TestGetAgentHooksDelegates(t *testing.T) {
	// We don't exercise the full hook merge here (claude tests cover it);
	// just ensure it doesn't blow up on a temp workspace and that the
	// method is wired (real hook install is exercised via claude delegation).
	plugin := &Plugin{resolvedBinary: "grok"}
	// Use a temp dir that won't exist as workspace; claude's impl will mkdir
	// what it needs under it for settings, but since we only care that the
	// call succeeds without "required" error, give a path.
	ws := t.TempDir()
	if err := plugin.GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{
		WorkspacePath: ws,
		SessionID:     "grok-test-1",
	}); err != nil {
		t.Fatalf("GetAgentHooks: %v", err)
	}
}
