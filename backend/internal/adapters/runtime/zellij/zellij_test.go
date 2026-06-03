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

func TestCommandBuilders(t *testing.T) {
	if got, want := versionArgs(), []string{"--version"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("versionArgs = %#v, want %#v", got, want)
	}
	if got, want := createSessionArgs("sess-1", "/tmp/layout.kdl"), []string{"attach", "--create-background", "sess-1", "options", "--default-layout", "/tmp/layout.kdl", "--pane-frames", "false", "--session-serialization", "false", "--show-startup-tips", "false", "--show-release-notes", "false"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("createSessionArgs = %#v, want %#v", got, want)
	}
	if got, want := listPanesArgs("sess-1"), []string{"--session", "sess-1", "action", "list-panes", "--all", "--json"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("listPanesArgs = %#v, want %#v", got, want)
	}
	if got, want := pasteArgs("sess-1", "terminal_0", "hello"), []string{"--session", "sess-1", "action", "paste", "--pane-id", "terminal_0", "hello"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("pasteArgs = %#v, want %#v", got, want)
	}
	if got, want := dumpScreenArgs("sess-1", "terminal_0"), []string{"--session", "sess-1", "action", "dump-screen", "--pane-id", "terminal_0", "--full"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("dumpScreenArgs = %#v, want %#v", got, want)
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

func TestBuildLayoutExportsEnvAndKeepsPaneAlive(t *testing.T) {
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

	for _, want := range []string{
		`cwd "/tmp/ws"`,
		`pane command="/bin/zsh" name="agent"`,
		"export AO_SESSION_ID='sess-1';",
		"export ODD='can'\\\\''t';",
		"export PATH='/custom/bin:/usr/bin';",
		"'ao' 'run'; exec '/bin/zsh' -i",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("layout missing %q in %q", want, got)
		}
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

	for _, want := range []string{
		`pane command="C:\\Program Files\\PowerShell\\7\\pwsh.exe" name="agent"`,
		`args "-NoLogo" "-NoProfile" "-NoExit" "-Command"`,
		"$env:AO_SESSION_ID = 'sess-1';",
		"$env:PATH = ",
		"& 'Write-Host' 'ready'",
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

	for _, want := range []string{
		`pane command="C:\\Windows\\System32\\cmd.exe" name="agent"`,
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
	fr := &fakeRunner{outputs: [][]byte{[]byte("zellij 0.44.3"), nil, []byte(`[{"id":0,"is_plugin":true,"title":"zellij:tab-bar"},{"id":3,"is_plugin":false,"title":"agent"}]`)}}
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
	if len(fr.calls) != 3 {
		t.Fatalf("calls = %d, want 3", len(fr.calls))
	}
	if got, want := fr.calls[0].args, []string{"--config-dir", "/tmp/cfg", "--version"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("version args = %#v, want %#v", got, want)
	}
	if got := fr.calls[1].args[:5]; !reflect.DeepEqual(got, []string{"--config-dir", "/tmp/cfg", "attach", "--create-background", "sess-1"}) {
		t.Fatalf("create args prefix = %#v", got)
	}
	if got := fr.calls[2].args; !reflect.DeepEqual(got, append([]string{"--config-dir", "/tmp/cfg"}, listPanesArgs("sess-1")...)) {
		t.Fatalf("list panes args = %#v", got)
	}
	if got, want := fr.calls[0].env, []string{"ZELLIJ_SOCKET_DIR=/tmp/zj"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("env = %#v, want %#v", got, want)
	}
}

func TestAttachCommandUsesSocketDir(t *testing.T) {
	r := New(Options{SocketDir: "/tmp/zj"})
	args, err := r.AttachCommand(ports.RuntimeHandle{ID: "sess-1/terminal_0"})
	if err != nil {
		t.Fatalf("AttachCommand: %v", err)
	}
	if runtime.GOOS == "windows" {
		if len(args) < 4 || args[0] != "powershell.exe" {
			t.Fatalf("attach command = %#v, want powershell wrapper", args)
		}
		if !strings.Contains(strings.Join(args, " "), "ZELLIJ_SOCKET_DIR") {
			t.Fatalf("attach command missing socket dir env: %#v", args)
		}
		return
	}
	if got, want := args[:3], []string{"env", "ZELLIJ_SOCKET_DIR=/tmp/zj", r.binary}; !reflect.DeepEqual(got, want) {
		t.Fatalf("attach prefix = %#v, want %#v", got, want)
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

func TestIsAliveTreatsExitStatusAsNotAlive(t *testing.T) {
	fr := &fakeRunner{err: &exec.ExitError{}}
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

func TestDestroyIsIdempotentWhenSessionMissing(t *testing.T) {
	fr := &fakeRunner{err: &exec.ExitError{}}
	r := New(Options{Timeout: time.Second})
	r.runner = fr

	if err := r.Destroy(context.Background(), ports.RuntimeHandle{ID: "sess-1/terminal_0"}); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if len(fr.calls) != 1 || fr.calls[0].args[0] != "kill-session" {
		t.Fatalf("calls = %#v, want only kill-session", fr.calls)
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
