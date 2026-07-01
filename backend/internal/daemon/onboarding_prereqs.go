package daemon

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// prereqProbeTimeout bounds the one `gh auth token` exec the prereqs probe runs.
const prereqProbeTimeout = 3 * time.Second

// emitPrereqsTelemetry runs the onboarding prereqs probe and emits funnel
// events: ao.onboarding.prereqs_checked (per-check booleans) every boot until
// the install reaches readiness, and ao.onboarding.prereqs_ready exactly once
// when every required tool is present. It is a deliberately lightweight
// pass/fail mirror of `ao doctor` (no versions): the app supervisor starts the
// daemon on first launch, so this seeds stage 4 of the app funnel without a
// first-run wizard. Skips entirely once readiness is already recorded so a
// healthy install pays nothing on later boots.
func emitPrereqsTelemetry(ctx context.Context, sink ports.EventSink, milestones *milestoneStore, alreadyReady func() bool) {
	if sink == nil || milestones == nil {
		return
	}
	if alreadyReady() {
		return
	}
	gitOK := binaryInPath("git")
	runtimeOK := runtime.GOOS == "windows" || binaryInPath("tmux")
	harnessOK := binaryInPath("claude") || binaryInPath("codex")
	githubOK := githubTokenAvailable(ctx)
	allOK := gitOK && runtimeOK && harnessOK && githubOK

	sink.Emit(ctx, ports.TelemetryEvent{
		Name:       "ao.onboarding.prereqs_checked",
		Source:     "daemon",
		OccurredAt: time.Now().UTC(),
		Level:      ports.TelemetryLevelInfo,
		Payload: map[string]any{
			"git_ok":     gitOK,
			"runtime_ok": runtimeOK,
			"harness_ok": harnessOK,
			"github_ok":  githubOK,
			"all_ok":     allOK,
		},
	})
	if allOK && milestones.claim("prereqs_ready") {
		sink.Emit(ctx, ports.TelemetryEvent{
			Name:       "ao.onboarding.prereqs_ready",
			Source:     "daemon",
			OccurredAt: time.Now().UTC(),
			Level:      ports.TelemetryLevelInfo,
			Payload:    map[string]any{},
		})
	}
}

func binaryInPath(name string) bool {
	path, err := exec.LookPath(name)
	return err == nil && path != ""
}

// githubTokenAvailable reports whether AO can authenticate to GitHub: an env
// token or a usable `gh auth token`. Mirrors cli.commandContext.githubToken
// without importing the cli package (daemon must not depend on cli).
func githubTokenAvailable(ctx context.Context) bool {
	for _, name := range []string{"AO_GITHUB_TOKEN", "GITHUB_TOKEN"} {
		if strings.TrimSpace(os.Getenv(name)) != "" {
			return true
		}
	}
	gh, err := exec.LookPath("gh")
	if err != nil || gh == "" {
		return false
	}
	probeCtx, cancel := context.WithTimeout(ctx, prereqProbeTimeout)
	defer cancel()
	out, err := exec.CommandContext(probeCtx, gh, "auth", "token").Output()
	return err == nil && strings.TrimSpace(string(out)) != ""
}
