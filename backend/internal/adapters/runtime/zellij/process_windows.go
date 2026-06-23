//go:build windows

package zellij

import (
	"os/exec"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

// hideWindow suppresses the console window for a console-subsystem child so
// frequent zellij calls (e.g. the reaper's list-sessions) don't flash on Windows.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NO_WINDOW,
	}
}

func startBackgroundProcess(env []string, name string, args ...string) error {
	script := "Start-Process -FilePath " + psQuote(name) + " -ArgumentList " + psQuote(windowsCommandLine(args)) + " -WindowStyle Hidden"
	cmd := exec.Command("powershell.exe", "-NoLogo", "-NoProfile", "-EncodedCommand", powerShellEncodedCommand(script))
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{
		// CREATE_NO_WINDOW, not CREATE_NEW_CONSOLE: the latter creates a console
		// then hides it, which flashed a window.
		CreationFlags: windows.CREATE_NO_WINDOW,
		HideWindow:    true,
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

func windowsCommandLine(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = windowsQuoteArg(arg)
	}
	return strings.Join(quoted, " ")
}

func windowsQuoteArg(arg string) string {
	if arg == "" {
		return `""`
	}
	if !strings.ContainsAny(arg, " \t\"") {
		return arg
	}

	var b strings.Builder
	b.WriteByte('"')
	backslashes := 0
	for _, r := range arg {
		switch r {
		case '\\':
			backslashes++
		case '"':
			b.WriteString(strings.Repeat(`\`, backslashes*2+1))
			b.WriteRune(r)
			backslashes = 0
		default:
			if backslashes > 0 {
				b.WriteString(strings.Repeat(`\`, backslashes))
				backslashes = 0
			}
			b.WriteRune(r)
		}
	}
	if backslashes > 0 {
		b.WriteString(strings.Repeat(`\`, backslashes*2))
	}
	b.WriteByte('"')
	return b.String()
}
