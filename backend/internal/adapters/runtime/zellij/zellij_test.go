package zellij

import (
	"context"
	"errors"
	"os/exec"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestNewDefaultsToPortableShell(t *testing.T) {
	t.Setenv("SHELL", "")
	r := New(Options{})
	want := "/bin/sh"
	if runtime.GOOS == "windows" {
		want = "powershell.exe"
	}
	if got := r.shell; got != want {
		t.Fatalf("default shell = %q, want %q", got, want)
	}
}

func TestZellijCommandEnvNormalizesBrowserTerminalColors(t *testing.T) {
	got := zellijCommandEnv(
		[]string{"NO_COLOR=1", "TERM=dumb", "COLORTERM=", "KEEP=yes"},
		[]string{"ZELLIJ_SOCKET_DIR=/tmp/zj"},
	)

	if runtime.GOOS == "windows" {
		if !contains(got, "NO_COLOR=1") {
			t.Fatalf("windows env = %#v, want NO_COLOR preserved", got)
		}
		return
	}

	if containsKey(got, "NO_COLOR") {
		t.Fatalf("NO_COLOR survived env normalization: %#v", got)
	}
	for _, want := range []string{"KEEP=yes", "TERM=xterm-256color", "COLORTERM=truecolor", "ZELLIJ_SOCKET_DIR=/tmp/zj"} {
		if !contains(got, want) {
			t.Fatalf("env missing %q in %#v", want, got)
		}
	}
}

func expectedZellijEnv(socketDir string) []string {
	env := []string{}
	if runtime.GOOS != "windows" {
		env = append(env, "TERM=xterm-256color", "COLORTERM=truecolor")
	}
	if socketDir != "" {
		env = append(env, "ZELLIJ_SOCKET_DIR="+socketDir)
	}
	return env
}

func expectedAttachEnvPrefix() []string {
	if runtime.GOOS == "windows" {
		return []string{}
	}
	return []string{"env", "-u", "NO_COLOR", "TERM=xterm-256color", "COLORTERM=truecolor"}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func containsKey(values []string, key string) bool {
	prefix := key + "="
	for _, value := range values {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func TestCommandBuilders(t *testing.T) {
	embeddedOptions := []string{
		"--pane-frames", "false",
		"--mouse-mode", "false",
		"--advanced-mouse-actions", "false",
		"--mouse-hover-effects", "false",
		"--focus-follows-mouse", "false",
		"--mouse-click-through", "false",
		"--support-kitty-keyboard-protocol", "false",
	}
	if got, want := versionArgs(), []string{"--version"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("versionArgs = %#v, want %#v", got, want)
	}
	wantCreate := append([]string{"attach", "--create-background", "sess-1", "options", "--default-layout", "/tmp/layout.kdl"}, embeddedOptions...)
	wantCreate = append(wantCreate, "--session-serialization", "false", "--show-startup-tips", "false", "--show-release-notes", "false")
	if got, want := createSessionArgs("sess-1", "/tmp/layout.kdl"), wantCreate; !reflect.DeepEqual(got, want) {
		t.Fatalf("createSessionArgs = %#v, want %#v", got, want)
	}
	if got, want := listPanesArgs("sess-1"), []string{"--session", "sess-1", "action", "list-panes", "--all", "--json"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("listPanesArgs = %#v, want %#v", got, want)
	}
	pasteAction := "paste"
	if runtime.GOOS == "windows" {
		pasteAction = "write-chars"
	}
	if got, want := pasteArgs("sess-1", "terminal_0", "hello"), []string{"--session", "sess-1", "action", pasteAction, "--pane-id", "terminal_0", "hello"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("pasteArgs = %#v, want %#v", got, want)
	}
	if got, want := dumpScreenArgs("sess-1", "terminal_0"), []string{"--session", "sess-1", "action", "dump-screen", "--pane-id", "terminal_0", "--full"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("dumpScreenArgs = %#v, want %#v", got, want)
	}
	// delete-session --force (not kill-session): teardown must also purge the
	// serialized resurrection state, or a later `zellij attach` re-creates the
	// session — and re-runs its agent — after the daemon destroyed it.
	if got, want := deleteSessionArgs("sess-1"), []string{"delete-session", "--force", "sess-1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("deleteSessionArgs = %#v, want %#v", got, want)
	}
	wantAttach := append([]string{"attach", "sess-1", "options"}, embeddedOptions...)
	if got, want := attachArgs("sess-1"), wantAttach; !reflect.DeepEqual(got, want) {
		t.Fatalf("attachArgs = %#v, want %#v", got, want)
	}
}

func TestZellijSessionNameSanitizesIssueRefs(t *testing.T) {
	got, err := zellijSessionName("repo/issue#42.1")
	if err != nil {
		t.Fatalf("zellijSessionName: %v", err)
	}
	if err := validateSessionID(got); err != nil {
		t.Fatalf("sanitized id %q is invalid: %v", got, err)
	}
	if !strings.HasPrefix(got, "repo-issue-42-1-") {
		t.Fatalf("sanitized id = %q, want readable prefix", got)
	}
	if got == "repo/issue#42.1" {
		t.Fatal("sanitized id still contains raw unsafe characters")
	}
}

// SessionName must return the exact name Create registers a session under, so
// callers that print an attach hint (e.g. `ao spawn`) reference the real
// session. A short, conforming id passes through; a long one is sanitised to a
// different name — printing the raw id there would send users to a missing
// session.
func TestSessionNameMatchesCreateNaming(t *testing.T) {
	short := "myproj-1"
	if got := SessionName(short); got != short {
		t.Fatalf("SessionName(%q) = %q, want it unchanged", short, got)
	}

	long := domain.SessionID(strings.Repeat("x", 60) + "-1")
	viaCreate, err := zellijSessionName(long)
	if err != nil {
		t.Fatalf("zellijSessionName: %v", err)
	}
	if got := SessionName(string(long)); got != viaCreate {
		t.Fatalf("SessionName = %q, but Create uses %q", got, viaCreate)
	}
	if SessionName(string(long)) == string(long) {
		t.Fatal("expected a long id to be sanitised to a different name")
	}
}

func TestValidateSessionAndPaneID(t *testing.T) {
	for _, id := range []string{"sess-1", "S_2", "abc123"} {
		if err := validateSessionID(id); err != nil {
			t.Fatalf("validateSessionID(%q): %v", id, err)
		}
	}
	for _, id := range []string{"", "sess.1", "sess/1", "$(boom)", "with space"} {
		if err := validateSessionID(id); err == nil {
			t.Fatalf("validateSessionID(%q): got nil, want error", id)
		}
	}
	for _, id := range []string{"terminal_0", "terminal_42"} {
		if err := validatePaneID(id); err != nil {
			t.Fatalf("validatePaneID(%q): %v", id, err)
		}
	}
	for _, id := range []string{"", "0", "plugin_0", "terminal_x", "terminal_1/2"} {
		if err := validatePaneID(id); err == nil {
			t.Fatalf("validatePaneID(%q): got nil, want error", id)
		}
	}
}

func TestHandleID(t *testing.T) {
	session, pane, err := handleID(ports.RuntimeHandle{ID: "sess-1/terminal_7"})
	if err != nil {
		t.Fatalf("handleID: %v", err)
	}
	if session != "sess-1" || pane != "terminal_7" {
		t.Fatalf("handleID = %q/%q", session, pane)
	}
}

func TestBuildLayoutExportsEnvAndRunsAgentCommand(t *testing.T) {
	oldGetenv := getenv
	getenv = func(key string) string {
		if key == "PATH" {
			return "/usr/bin:/bin"
		}
		return ""
	}
	defer func() { getenv = oldGetenv }()

	got := buildLayout(ports.RuntimeConfig{WorkspacePath: "/tmp/ws", Argv: []string{"ao", "run"}, Env: map[string]string{
		"AO_SESSION_ID": "sess-1",
		"ODD":           "can't",
		"PATH":          "/custom/bin:/usr/bin",
	}}, "/bin/zsh")
	if runtime.GOOS == "windows" {
		for _, want := range []string{
			`cwd "/tmp/ws"`,
			`pane command="ao" name="agent" borderless=true`,
			`args "run"`,
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("direct windows layout missing %q in %q", want, got)
			}
		}
		return
	}

	for _, want := range []string{
		`cwd "/tmp/ws"`,
		`pane command="/bin/zsh" name="agent" borderless=true`,
		"export AO_SESSION_ID='sess-1';",
		"export ODD='can'\\\\''t';",
		"export PATH='/custom/bin:/usr/bin';",
		"'ao' 'run'",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("layout missing %q in %q", want, got)
		}
	}
	if strings.Contains(got, "exec '/bin/zsh' -i") {
		t.Fatalf("layout kept pane alive after agent exit: %q", got)
	}
}

func TestBuildLayoutUsesPowerShellLaunchOnWindowsShells(t *testing.T) {
	oldGetenv := getenv
	getenv = func(key string) string {
		if key == "PATH" {
			return `C:\custom\bin`
		}
		return ""
	}
	defer func() { getenv = oldGetenv }()

	got := buildLayout(ports.RuntimeConfig{WorkspacePath: `C:\ws`, Argv: []string{"Write-Host", "ready"}, Env: map[string]string{
		"AO_SESSION_ID": "sess-1",
	}}, `C:\Program Files\PowerShell\7\pwsh.exe`)
	if runtime.GOOS == "windows" {
		for _, want := range []string{
			`cwd "C:\\ws"`,
			`pane command="Write-Host" name="agent" borderless=true`,
			`args "ready"`,
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("direct windows layout missing %q in %q", want, got)
			}
		}
		return
	}

	for _, want := range []string{
		`pane command="C:\\Program Files\\PowerShell\\7\\pwsh.exe" name="agent" borderless=true`,
		`args "-NoLogo" "-NoProfile" "-NoExit" "-EncodedCommand"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("powershell layout missing %q in %q", want, got)
		}
	}
}

func TestBuildLayoutUsesCmdLaunchOnCmdShells(t *testing.T) {
	oldGetenv := getenv
	getenv = func(key string) string {
		return ""
	}
	defer func() { getenv = oldGetenv }()

	got := buildLayout(ports.RuntimeConfig{WorkspacePath: `C:\ws`, Argv: []string{"echo", "ready"}, Env: map[string]string{
		"AO_SESSION_ID": "sess-1",
	}}, `C:\Windows\System32\cmd.exe`)
	if runtime.GOOS == "windows" {
		for _, want := range []string{
			`cwd "C:\\ws"`,
			`pane command="echo" name="agent" borderless=true`,
			`args "ready"`,
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("direct windows layout missing %q in %q", want, got)
			}
		}
		return
	}

	for _, want := range []string{
		`pane command="C:\\Windows\\System32\\cmd.exe" name="agent" borderless=true`,
		`args "/D" "/S" "/K"`,
		`AO_SESSION_ID=sess-1`,
		`\"echo\" \"ready\"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("cmd layout missing %q in %q", want, got)
		}
	}
}

func TestCreateRejectsInvalidEnvKeys(t *testing.T) {
	r := New(Options{Binary: "zellij-test", Timeout: time.Second, Shell: "/bin/zsh"})
	r.runner = &fakeRunner{}
	_, err := r.Create(context.Background(), ports.RuntimeConfig{
		SessionID:     "sess-1",
		WorkspacePath: "/tmp/ws",
		Argv:          []string{"echo", "ready"},
		Env:           map[string]string{"BAD KEY": "x"},
	})
	if err == nil || !strings.Contains(err.Error(), "invalid env key") {
		t.Fatalf("Create err = %v, want invalid env key", err)
	}
}

func TestCreateStartsSessionAndDiscoversPane(t *testing.T) {
	panesOut := []byte(`[{"id":0,"is_plugin":true,"title":"zellij:tab-bar"},{"id":3,"is_plugin":false,"title":"agent"}]`)
	outputs := [][]byte{
		[]byte("zellij 0.44.3"),
		nil,
		nil,
		panesOut,
	}
	if runtime.GOOS == "windows" {
		outputs = append(outputs, panesOut)
	}
	outputs = append(outputs, []byte("sess-1 [Created 1s ago] \n"))
	fr := &fakeRunner{outputs: outputs}
	r := New(Options{Binary: "zellij-test", Timeout: time.Second, Shell: "/bin/zsh", SocketDir: "/tmp/zj", ConfigDir: "/tmp/cfg"})
	r.runner = fr

	handle, err := r.Create(context.Background(), ports.RuntimeConfig{
		SessionID:     "sess-1",
		WorkspacePath: "/tmp/ws",
		Argv:          []string{"echo", "ready"},
		Env:           map[string]string{"AO_SESSION_ID": "sess-1"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if handle != (ports.RuntimeHandle{ID: "sess-1/terminal_3"}) {
		t.Fatalf("handle = %+v, want zellij handle", handle)
	}
	wantCalls := 5
	if runtime.GOOS == "windows" {
		wantCalls = 6
	}
	if len(fr.calls) != wantCalls {
		t.Fatalf("calls = %d, want %d", len(fr.calls), wantCalls)
	}
	if got, want := fr.calls[0].args, []string{"--config-dir", "/tmp/cfg", "--version"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("version args = %#v, want %#v", got, want)
	}
	if got, want := fr.calls[1].args, []string{"--config-dir", "/tmp/cfg", "delete-session", "--force", "sess-1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("delete args = %#v, want %#v", got, want)
	}
	if got := fr.calls[2].args[:5]; !reflect.DeepEqual(got, []string{"--config-dir", "/tmp/cfg", "attach", "--create-background", "sess-1"}) {
		t.Fatalf("create args prefix = %#v", got)
	}
	if got := fr.calls[3].args; !reflect.DeepEqual(got, append([]string{"--config-dir", "/tmp/cfg"}, listPanesArgs("sess-1")...)) {
		t.Fatalf("list panes args = %#v", got)
	}
	listSessionsCall := 4
	if runtime.GOOS == "windows" {
		if got := fr.calls[4].args; !reflect.DeepEqual(got, append([]string{"--config-dir", "/tmp/cfg"}, listPanesArgs("sess-1")...)) {
			t.Fatalf("ready list panes args = %#v", got)
		}
		listSessionsCall = 5
	}
	if got := fr.calls[listSessionsCall].args; !reflect.DeepEqual(got, append([]string{"--config-dir", "/tmp/cfg"}, listSessionsArgs()...)) {
		t.Fatalf("list sessions args = %#v", got)
	}
	if got, want := fr.calls[0].env, expectedZellijEnv("/tmp/zj"); !reflect.DeepEqual(got, want) {
		t.Fatalf("env = %#v, want %#v", got, want)
	}
}

func TestCreateClearsStaleSessionBeforeCreating(t *testing.T) {
	panesOut := []byte(`[{"id":1,"is_plugin":false,"title":"agent"}]`)
	outputs := [][]byte{
		[]byte("zellij 0.44.3"),
		nil,
		nil,
		panesOut,
	}
	if runtime.GOOS == "windows" {
		outputs = append(outputs, panesOut)
	}
	outputs = append(outputs, []byte("sess-1 [Created 1s ago] \n"))
	fr := &fakeRunner{outputs: outputs}
	r := New(Options{Binary: "zellij-test", Timeout: time.Second, Shell: "/bin/zsh"})
	r.runner = fr

	if _, err := r.Create(context.Background(), ports.RuntimeConfig{
		SessionID:     "sess-1",
		WorkspacePath: "/tmp/ws",
		Argv:          []string{"echo", "ready"},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	wantCalls := 5
	if runtime.GOOS == "windows" {
		wantCalls = 6
	}
	if len(fr.calls) != wantCalls {
		t.Fatalf("calls = %d, want %d", len(fr.calls), wantCalls)
	}
	if got, want := fr.calls[1].args, []string{"delete-session", "--force", "sess-1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("delete args = %#v, want %#v", got, want)
	}
	if got := fr.calls[2].args[:3]; !reflect.DeepEqual(got, []string{"attach", "--create-background", "sess-1"}) {
		t.Fatalf("create args prefix = %#v", got)
	}
}

func TestAttachCommandUsesEmbeddedClientOptions(t *testing.T) {
	r := New(Options{})
	args, _, err := r.AttachCommand(ports.RuntimeHandle{ID: "sess-1/terminal_0"})
	if err != nil {
		t.Fatalf("AttachCommand: %v", err)
	}
	embeddedOptions := []string{
		"--pane-frames", "false",
		"--mouse-mode", "false",
		"--advanced-mouse-actions", "false",
		"--mouse-hover-effects", "false",
		"--focus-follows-mouse", "false",
		"--mouse-click-through", "false",
		"--support-kitty-keyboard-protocol", "false",
	}
	if runtime.GOOS == "windows" {
		joined := strings.Join(args, " ")
		for _, want := range embeddedOptions {
			if !strings.Contains(joined, want) {
				t.Fatalf("windows attach command missing %q: %#v", want, args)
			}
		}
		return
	}
	want := append(expectedAttachEnvPrefix(), r.binary, "attach", "sess-1", "options")
	want = append(want, embeddedOptions...)
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("AttachCommand = %#v, want %#v", args, want)
	}
}

func TestAttachCommandUsesSocketDir(t *testing.T) {
	r := New(Options{SocketDir: "/tmp/zj"})
	args, _, err := r.AttachCommand(ports.RuntimeHandle{ID: "sess-1/terminal_0"})
	if err != nil {
		t.Fatalf("AttachCommand: %v", err)
	}
	if runtime.GOOS == "windows" {
		if got, want := args[0], r.binary; got != want {
			t.Fatalf("attach binary = %q, want %q", got, want)
		}
		return
	}
	if got, want := args[:6], []string{"env", "-u", "NO_COLOR", "TERM=xterm-256color", "COLORTERM=truecolor", "ZELLIJ_SOCKET_DIR=/tmp/zj"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("attach prefix = %#v, want %#v", got, want)
	}
	if got, want := args[6], r.binary; got != want {
		t.Fatalf("attach binary = %q, want %q", got, want)
	}
}

func TestFindAgentPaneRetriesTransientErrors(t *testing.T) {
	fr := &fakeRunner{outputs: [][]byte{[]byte("boom"), []byte(`[{"id":0,"is_plugin":false,"title":"agent"}]`)}}
	r := New(Options{Timeout: time.Second})
	r.runner = fr

	got, err := r.findAgentPane(context.Background(), "sess-1")
	if err != nil {
		t.Fatalf("findAgentPane: %v", err)
	}
	if got != "terminal_0" {
		t.Fatalf("findAgentPane = %q, want terminal_0", got)
	}
	if len(fr.calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(fr.calls))
	}
}

func TestParseVersion(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want semver
	}{
		{in: "zellij 0.44.3", want: semver{0, 44, 3}},
		{in: "zellij v1.2.3\n", want: semver{1, 2, 3}},
		{in: "zellij 0.44.3-dev", want: semver{0, 44, 3}},
	} {
		got, err := parseVersion(tc.in)
		if err != nil {
			t.Fatalf("parseVersion(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("parseVersion(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
	if _, err := parseVersion("zellij nope"); err == nil {
		t.Fatal("parseVersion invalid: got nil, want error")
	}
	if compareVersion(semver{0, 44, 2}, semver{0, 44, 3}) >= 0 {
		t.Fatal("compareVersion should order 0.44.2 before 0.44.3")
	}
	if got := RequiredVersion(); got != "0.44.3" {
		t.Fatalf("RequiredVersion = %q, want 0.44.3", got)
	}
	if got, err := CheckVersionOutput("zellij 0.44.3"); err != nil || got != "0.44.3" {
		t.Fatalf("CheckVersionOutput supported = %q, %v", got, err)
	}
	if _, err := CheckVersionOutput("zellij 0.44.2"); err == nil {
		t.Fatal("CheckVersionOutput unsupported: got nil error")
	}
}

func TestSendMessageChunksAndSendsEnter(t *testing.T) {
	fr := &fakeRunner{}
	r := New(Options{Timeout: time.Second, ChunkSize: 5})
	r.runner = fr

	if err := r.SendMessage(context.Background(), ports.RuntimeHandle{ID: "sess-1/terminal_0"}, "hello世界"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if len(fr.calls) != 4 {
		t.Fatalf("calls = %d, want 4", len(fr.calls))
	}
	if got, want := fr.calls[0].args, pasteArgs("sess-1", "terminal_0", "hello"); !reflect.DeepEqual(got, want) {
		t.Fatalf("paste 1 args = %#v, want %#v", got, want)
	}
	if got, want := fr.calls[1].args, pasteArgs("sess-1", "terminal_0", "世"); !reflect.DeepEqual(got, want) {
		t.Fatalf("paste 2 args = %#v, want %#v", got, want)
	}
	if got, want := fr.calls[2].args, pasteArgs("sess-1", "terminal_0", "界"); !reflect.DeepEqual(got, want) {
		t.Fatalf("paste 3 args = %#v, want %#v", got, want)
	}
	if got, want := fr.calls[3].args, sendEnterArgs("sess-1", "terminal_0"); !reflect.DeepEqual(got, want) {
		t.Fatalf("enter args = %#v, want %#v", got, want)
	}
}

func TestGetOutputTrimsLines(t *testing.T) {
	fr := &fakeRunner{outputs: [][]byte{[]byte("one\ntwo\nthree\n")}}
	r := New(Options{Timeout: time.Second})
	r.runner = fr

	out, err := r.GetOutput(context.Background(), ports.RuntimeHandle{ID: "sess-1/terminal_0"}, 2)
	if err != nil {
		t.Fatalf("GetOutput: %v", err)
	}
	if out != "two\nthree\n" {
		t.Fatalf("output = %q, want last two lines", out)
	}
}

func TestGetOutputTrimsTrailingScreenPaddingBeforeTailing(t *testing.T) {
	fr := &fakeRunner{outputs: [][]byte{[]byte("ready\nprompt> echo hi\nhi\n\n\n\n")}}
	r := New(Options{Timeout: time.Second})
	r.runner = fr

	out, err := r.GetOutput(context.Background(), ports.RuntimeHandle{ID: "sess-1/terminal_0"}, 2)
	if err != nil {
		t.Fatalf("GetOutput: %v", err)
	}
	if out != "prompt> echo hi\nhi\n" {
		t.Fatalf("output = %q, want last non-padding lines", out)
	}
}

func TestIsAliveParsesNoFormattingOutput(t *testing.T) {
	fr := &fakeRunner{outputs: [][]byte{[]byte("sess-1 [Created 1s ago] \nold [Created 2s ago] (EXITED - attach to resurrect)\n")}}
	r := New(Options{Timeout: time.Second})
	r.runner = fr

	alive, err := r.IsAlive(context.Background(), ports.RuntimeHandle{ID: "sess-1/terminal_0"})
	if err != nil {
		t.Fatalf("IsAlive: %v", err)
	}
	if !alive {
		t.Fatal("alive = false, want true")
	}
	if sessionListedAlive("sess-1-long [Created 1s ago]", "sess-1") {
		t.Fatal("prefix matched as alive")
	}
	if sessionListedAlive("sess-1 [Created 1s ago] (EXITED - attach to resurrect)", "sess-1") {
		t.Fatal("exited session matched as alive")
	}
}

// IsAlive may treat a non-zero list-sessions ONLY as "not alive" when zellij
// says no sessions exist at all. Any other exit failure is a probe error: the
// reaper reports it as a failed probe and the LCM must never read it as death.
func TestIsAliveTreatsNoSessionsExitAsNotAlive(t *testing.T) {
	fr := &fakeRunner{outputs: [][]byte{[]byte("No active zellij sessions found.")}, err: &exec.ExitError{}}
	r := New(Options{Timeout: time.Second})
	r.runner = fr

	alive, err := r.IsAlive(context.Background(), ports.RuntimeHandle{ID: "sess-1/terminal_0"})
	if err != nil {
		t.Fatalf("IsAlive: %v", err)
	}
	if alive {
		t.Fatal("alive = true, want false")
	}
}

func TestIsAliveReportsOtherExitFailuresAsProbeErrors(t *testing.T) {
	fr := &fakeRunner{outputs: [][]byte{[]byte("thread 'main' panicked")}, err: &exec.ExitError{}}
	r := New(Options{Timeout: time.Second})
	r.runner = fr

	alive, err := r.IsAlive(context.Background(), ports.RuntimeHandle{ID: "sess-1/terminal_0"})
	if err == nil {
		t.Fatal("IsAlive: got nil error, want probe failure — a failed probe must not read as dead")
	}
	if alive {
		t.Fatal("alive = true on probe failure")
	}
}

func TestDestroyIsIdempotentWhenSessionMissing(t *testing.T) {
	fr := &fakeRunner{outputs: [][]byte{[]byte("No active zellij sessions found.")}, err: &exec.ExitError{}}
	r := New(Options{Timeout: time.Second})
	r.runner = fr

	if err := r.Destroy(context.Background(), ports.RuntimeHandle{ID: "sess-1/terminal_0"}); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if len(fr.calls) != 1 || fr.calls[0].args[0] != "delete-session" {
		t.Fatalf("calls = %#v, want only delete-session", fr.calls)
	}
}

func TestDestroyReportsUnexpectedExitFailures(t *testing.T) {
	fr := &fakeRunner{outputs: [][]byte{[]byte("permission denied")}, err: &exec.ExitError{}}
	r := New(Options{Timeout: time.Second})
	r.runner = fr

	if err := r.Destroy(context.Background(), ports.RuntimeHandle{ID: "sess-1/terminal_0"}); err == nil {
		t.Fatal("Destroy: got nil, want unexpected delete-session failure")
	}
}

// Destroy must delete the session's serialized state, not merely kill it: a
// killed-but-cached session is resurrected (agent re-run included) by any later
// `zellij attach`, bringing a terminated session's runtime back to life.
func TestDestroyForceDeletesSerializedSession(t *testing.T) {
	fr := &fakeRunner{}
	r := New(Options{Timeout: time.Second})
	r.runner = fr

	if err := r.Destroy(context.Background(), ports.RuntimeHandle{ID: "sess-1/terminal_0"}); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if len(fr.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(fr.calls))
	}
	if got, want := fr.calls[0].args, []string{"delete-session", "--force", "sess-1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("destroy args = %#v, want %#v", got, want)
	}
}

func TestGetOutputValidatesLines(t *testing.T) {
	r := New(Options{Timeout: time.Second})
	_, err := r.GetOutput(context.Background(), ports.RuntimeHandle{ID: "sess-1/terminal_0"}, 0)
	if err == nil {
		t.Fatal("GetOutput lines=0: got nil, want error")
	}
}

type fakeRunner struct {
	calls   []runnerCall
	outputs [][]byte
	err     error
}

type runnerCall struct {
	env  []string
	name string
	args []string
}

func (f *fakeRunner) Run(_ context.Context, env []string, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, runnerCall{env: append([]string(nil), env...), name: name, args: append([]string(nil), args...)})
	var out []byte
	if len(f.outputs) > 0 {
		out = f.outputs[0]
		f.outputs = f.outputs[1:]
	}
	if f.err != nil {
		return out, f.err
	}
	return out, nil
}

func (f *fakeRunner) Start(env []string, name string, args ...string) error {
	f.calls = append(f.calls, runnerCall{env: append([]string(nil), env...), name: name, args: append([]string(nil), args...)})
	if len(f.outputs) > 0 {
		f.outputs = f.outputs[1:]
	}
	return f.err
}

func TestCommandErrorUnwraps(t *testing.T) {
	base := errors.New("base")
	err := commandError{err: base, output: "details"}
	if !errors.Is(err, base) {
		t.Fatal("commandError should unwrap base error")
	}
	if !strings.Contains(err.Error(), "details") {
		t.Fatalf("error = %q, want output details", err.Error())
	}
}
