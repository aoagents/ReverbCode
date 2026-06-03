package zellij

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

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
	return []string{
		"attach", "--create-background", id,
		"options",
		"--default-layout", layoutPath,
		"--pane-frames", "false",
		"--session-serialization", "false",
		"--show-startup-tips", "false",
		"--show-release-notes", "false",
	}
}

func listPanesArgs(id string) []string {
	return []string{"--session", id, "action", "list-panes", "--all", "--json"}
}

func pasteArgs(id, paneID, chunk string) []string {
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

func killSessionArgs(id string) []string {
	return []string{"kill-session", id}
}

func attachArgs(id string) []string {
	return []string{"attach", id}
}

func handleIDValue(sessionID, paneID string) string {
	return sessionID + "/" + paneID
}

func terminalPaneID(id int) string {
	return fmt.Sprintf("terminal_%d", id)
}

func buildLayout(cfg ports.RuntimeConfig, shellPath string) string {
	spec := shellLaunchSpecFor(shellPath)
	shellCommand := shellLaunchCommand(cfg, shellPath, spec)
	return layoutString(cfg.WorkspacePath, shellPath, spec.args, shellCommand)
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
		return shellLaunchSpec{args: []string{"-NoLogo", "-NoProfile", "-NoExit", "-Command"}}
	}
	return shellLaunchSpec{args: []string{"-lc"}}
}

func layoutString(workspacePath, shellPath string, shellArgs []string, shellCommand string) string {
	return "layout {\n" +
		"  cwd " + kdlQuote(workspacePath) + "\n" +
		"  pane command=" + kdlQuote(shellPath) + " name=" + kdlQuote(agentPaneName) + " {\n" +
		"    args " + kdlJoin(shellArgs) + " " + kdlQuote(shellCommand) + "\n" +
		"  }\n" +
		"}\n"
}

func shellLaunchCommand(cfg ports.RuntimeConfig, shellPath string, spec shellLaunchSpec) string {
	if len(spec.args) > 0 && spec.args[0] == "-NoLogo" {
		return wrapLaunchCommandPowerShell(cfg)
	}
	if len(spec.args) > 0 && spec.args[0] == "/D" {
		return wrapLaunchCommandCmd(cfg)
	}
	return wrapLaunchCommandUnix(cfg, shellPath)
}

func wrapLaunchCommandUnix(cfg ports.RuntimeConfig, shellPath string) string {
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
	b.WriteString("; exec ")
	b.WriteString(shellQuote(shellPath))
	b.WriteString(" -i")
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
	return b.String()
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
