package zellij

import (
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	agentPaneName     = "agent"
	defaultChunkBytes = 16 * 1024
)

func versionArgs() []string {
	return []string{"--version"}
}

func createSessionArgs(id, layoutPath string) []string {
	clientOptions := embeddedClientOptions()
	args := make([]string, 0, 6+len(clientOptions)+6)
	args = append(args,
		"attach", "--create-background", id,
		"options",
		"--default-layout", layoutPath,
	)
	args = append(args, clientOptions...)
	args = append(args,
		"--session-serialization", "false",
		"--show-startup-tips", "false",
		"--show-release-notes", "false",
	)
	return args
}

func listPanesArgs(id string) []string {
	return []string{"--session", id, "action", "list-panes", "--all", "--json"}
}

func pasteArgs(id, paneID, chunk string) []string {
	if runtime.GOOS == "windows" {
		return []string{"--session", id, "action", "write-chars", "--pane-id", paneID, chunk}
	}
	return []string{"--session", id, "action", "paste", "--pane-id", paneID, chunk}
}

func sendEnterArgs(id, paneID string) []string {
	return []string{"--session", id, "action", "send-keys", "--pane-id", paneID, "Enter"}
}

func dumpScreenArgs(id, paneID string) []string {
	return []string{"--session", id, "action", "dump-screen", "--pane-id", paneID, "--full"}
}

func listSessionsArgs() []string {
	return []string{"list-sessions", "--no-formatting"}
}

// deleteSessionArgs builds the teardown command. `delete-session --force`
// kills a running session AND removes its serialized resurrection state in one
// step. Plain `kill-session` is not enough: zellij can keep the session in its
// global resurrection cache as "(EXITED - attach to resurrect)", and any later
// `zellij attach <id>` (e.g. the terminal mux re-opening a pane) would resurrect
// it — re-running the agent command for a session the daemon already destroyed.
func deleteSessionArgs(id string) []string {
	return []string{"delete-session", "--force", id}
}

func attachArgs(id string) []string {
	clientOptions := embeddedClientOptions()
	args := make([]string, 0, 3+len(clientOptions))
	args = append(args,
		"attach", id,
		"options",
	)
	args = append(args, clientOptions...)
	return args
}

func embeddedClientOptions() []string {
	return []string{
		"--pane-frames", "false",
		"--mouse-mode", "false",
		"--advanced-mouse-actions", "false",
		"--mouse-hover-effects", "false",
		"--focus-follows-mouse", "false",
		"--mouse-click-through", "false",
		"--support-kitty-keyboard-protocol", "false",
	}
}

func handleIDValue(sessionID, paneID string) string {
	return sessionID + "/" + paneID
}

func terminalPaneID(id int) string {
	return fmt.Sprintf("terminal_%d", id)
}

func buildLayout(cfg ports.RuntimeConfig, shellPath string) string {
	if runtime.GOOS == "windows" {
		return directLayoutString(cfg.WorkspacePath, cfg.Argv)
	}
	spec := shellLaunchSpecFor(shellPath)
	shellCommand := shellLaunchCommand(cfg, spec)
	return layoutString(cfg.WorkspacePath, shellPath, spec.args, shellCommand)
}

// windowsLaunchArgv returns the argv zellij executes on Windows to start the
// agent. The trampoline reads the launch spec from AO_LAUNCH_SPEC, so KDL
// args quoting cannot mangle codex's `--config key=value` flags.
func windowsLaunchArgv(launcherBinary string) []string {
	command := launcherBinary
	if command == "" {
		command = "ao"
	}
	return []string{command, "launch"}
}

func windowsFallbackShellArgv(shellPath string) []string {
	if strings.TrimSpace(shellPath) == "" {
		shellPath = "powershell.exe"
	}
	base := strings.ToLower(filepathBase(shellPath))
	if strings.Contains(base, "cmd") {
		return []string{shellPath, "/D", "/Q", "/K"}
	}
	if strings.Contains(base, "powershell") || strings.Contains(base, "pwsh") {
		return []string{shellPath, "-NoLogo", "-NoProfile", "-NoExit"}
	}
	if strings.Contains(base, "sh") {
		return []string{shellPath, "-i"}
	}
	return []string{shellPath}
}

// directLayoutString builds a layout that runs argv[0] with argv[1:] as zellij
// `args`, with no intermediate shell. Used on Windows where wrapping the agent
// in powershell/cmd quoting is unsound for arbitrary argv (e.g. codex's
// `--config key="value with spaces"`).
func directLayoutString(workspacePath string, argv []string) string {
	command := ""
	args := []string{}
	if len(argv) > 0 {
		command = argv[0]
		args = argv[1:]
	}

	var b strings.Builder
	b.WriteString("layout {\n")
	b.WriteString("  cwd ")
	b.WriteString(kdlQuote(workspacePath))
	b.WriteString("\n")
	b.WriteString("  pane command=")
	b.WriteString(kdlQuote(command))
	b.WriteString(" name=")
	b.WriteString(kdlQuote(agentPaneName))
	b.WriteString(" borderless=true {\n")
	if len(args) > 0 {
		b.WriteString("    args ")
		b.WriteString(kdlJoin(args))
		b.WriteString("\n")
	}
	b.WriteString("  }\n")
	b.WriteString("}\n")
	return b.String()
}

type shellLaunchSpec struct {
	args []string
}

func shellLaunchSpecFor(shellPath string) shellLaunchSpec {
	base := strings.ToLower(filepathBase(shellPath))
	if strings.Contains(base, "cmd") {
		return shellLaunchSpec{args: []string{"/D", "/S", "/K"}}
	}
	if strings.Contains(base, "powershell") || strings.Contains(base, "pwsh") {
		return shellLaunchSpec{args: []string{"-NoLogo", "-NoProfile", "-NoExit", "-EncodedCommand"}}
	}
	return shellLaunchSpec{args: []string{"-lc"}}
}

func layoutString(workspacePath, shellPath string, shellArgs []string, shellCommand string) string {
	return "layout {\n" +
		"  cwd " + kdlQuote(workspacePath) + "\n" +
		"  pane command=" + kdlQuote(shellPath) + " name=" + kdlQuote(agentPaneName) + " borderless=true {\n" +
		"    args " + kdlJoin(shellArgs) + " " + kdlQuote(shellCommand) + "\n" +
		"  }\n" +
		"}\n"
}

func shellLaunchCommand(cfg ports.RuntimeConfig, spec shellLaunchSpec) string {
	if len(spec.args) > 0 && spec.args[0] == "-NoLogo" {
		return wrapLaunchCommandPowerShell(cfg)
	}
	if len(spec.args) > 0 && spec.args[0] == "/D" {
		return wrapLaunchCommandCmd(cfg)
	}
	return wrapLaunchCommandUnix(cfg)
}

func wrapLaunchCommandUnix(cfg ports.RuntimeConfig) string {
	path := cfg.Env["PATH"]
	if path == "" {
		path = getenv("PATH")
	}

	var b strings.Builder
	for _, key := range sortedKeys(cfg.Env) {
		if key == "PATH" {
			continue
		}
		b.WriteString("export ")
		b.WriteString(key)
		b.WriteString("=")
		b.WriteString(shellQuote(cfg.Env[key]))
		b.WriteString("; ")
	}
	if path != "" {
		b.WriteString("export PATH=")
		b.WriteString(shellQuote(path))
		b.WriteString("; ")
	}
	b.WriteString(quoteArgvUnix(cfg.Argv))
	return b.String()
}

func wrapLaunchCommandPowerShell(cfg ports.RuntimeConfig) string {
	path := cfg.Env["PATH"]
	if path == "" {
		path = getenv("PATH")
	}

	var b strings.Builder
	for _, key := range sortedKeys(cfg.Env) {
		if key == "PATH" {
			continue
		}
		b.WriteString("$env:")
		b.WriteString(key)
		b.WriteString(" = ")
		b.WriteString(psQuote(cfg.Env[key]))
		b.WriteString("; ")
	}
	if path != "" {
		b.WriteString("$env:PATH = ")
		b.WriteString(psQuote(path))
		b.WriteString("; ")
	}
	b.WriteString(quoteArgvPowerShell(cfg.Argv))
	return powerShellEncodedCommand(b.String())
}

// powerShellEncodedCommand returns the base64'd UTF-16-LE form of script,
// suitable for `powershell.exe -EncodedCommand`. zellij's KDL `args` quoting
// is not robust enough to round-trip arbitrary PowerShell script text through
// a plain `-Command` argv slot, so we hand PowerShell a single opaque base64
// blob instead.
func powerShellEncodedCommand(script string) string {
	words := utf16.Encode([]rune(script))
	buf := make([]byte, len(words)*2)
	for i, word := range words {
		binary.LittleEndian.PutUint16(buf[i*2:], word)
	}
	return base64.StdEncoding.EncodeToString(buf)
}

func wrapLaunchCommandCmd(cfg ports.RuntimeConfig) string {
	path := cfg.Env["PATH"]
	if path == "" {
		path = getenv("PATH")
	}

	var b strings.Builder
	for _, key := range sortedKeys(cfg.Env) {
		if key == "PATH" {
			continue
		}
		b.WriteString("set \"")
		b.WriteString(key)
		b.WriteString("=")
		b.WriteString(cmdQuote(cfg.Env[key]))
		b.WriteString("\" && ")
	}
	if path != "" {
		b.WriteString("set \"PATH=")
		b.WriteString(cmdQuote(path))
		b.WriteString("\" && ")
	}
	b.WriteString(quoteArgvCmd(cfg.Argv))
	return b.String()
}

func validateEnvKeys(env map[string]string) error {
	for key := range env {
		if !validEnvKey(key) {
			return fmt.Errorf("zellij runtime: invalid env key %q", key)
		}
	}
	return nil
}

func validEnvKey(key string) bool {
	if key == "" {
		return false
	}
	for i, r := range key {
		if r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
			continue
		}
		if i > 0 && r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return true
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func cmdQuote(s string) string {
	return strings.ReplaceAll(s, "\"", "\"\"")
}

// quoteArgvUnix renders argv as a POSIX-shell command, single-quoting each
// argument so a value with spaces stays one word under `sh -lc`.
func quoteArgvUnix(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		parts[i] = shellQuote(a)
	}
	return strings.Join(parts, " ")
}

// quoteArgvPowerShell renders argv for `powershell -Command`. The call operator
// `&` is required so a quoted first token is invoked as a command rather than
// echoed as a string literal.
func quoteArgvPowerShell(argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	parts := make([]string, len(argv))
	for i, a := range argv {
		parts[i] = psQuote(a)
	}
	return "& " + strings.Join(parts, " ")
}

// quoteArgvCmd renders argv for cmd.exe, wrapping each argument in double quotes
// (doubling any embedded quote) so spaces don't split a single argument.
func quoteArgvCmd(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		parts[i] = "\"" + strings.ReplaceAll(a, "\"", "\"\"") + "\""
	}
	return strings.Join(parts, " ")
}

func kdlQuote(s string) string {
	return strconv.Quote(s)
}

func kdlJoin(args []string) string {
	parts := make([]string, 0, len(args))
	for _, arg := range args {
		parts = append(parts, kdlQuote(arg))
	}
	return strings.Join(parts, " ")
}

func filepathBase(path string) string {
	if path == "" {
		return ""
	}
	i := strings.LastIndexAny(path, `/\`)
	if i < 0 {
		return path
	}
	return path[i+1:]
}
