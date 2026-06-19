package legacyimport

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// migratableHarnesses are the orchestrator harnesses the importer ports. aider
// (and anything else) is skipped with a note (gist §6).
var migratableHarnesses = map[string]bool{
	"claude-code": true,
	"codex":       true,
	"opencode":    true,
}

// terminalStates are the legacy canonical states that mean "do not import".
var terminalStates = map[string]bool{"done": true, "terminated": true}

// orchestratorStatus is the outcome of mapping one project's orchestrator.
type orchestratorStatus string

const (
	orchMapped  orchestratorStatus = "mapped"
	orchSkipped orchestratorStatus = "skipped"
	orchAbsent  orchestratorStatus = "absent"
)

// transcriptRelocation carries the inputs to relocate a claude-code transcript.
type transcriptRelocation struct {
	worktree string // legacy worktree path on disk (realpath-resolved by the relocator)
	uuid     string // claudeSessionUuid = the transcript filename stem
}

// orchestratorMapping is the mapped orchestrator session plus its transcript
// relocation (claude-code only) and any skip/lossy note.
type orchestratorMapping struct {
	projectID  string
	prefix     string
	status     orchestratorStatus
	record     domain.SessionRecord // valid when status == orchMapped
	transcript *transcriptRelocation
	note       string
}

// asObject coerces a JSON value that may be an object OR a JSON-encoded string
// into a decoded map, mirroring the legacy reader's double-decode.
func asObject(v any) map[string]any {
	switch t := v.(type) {
	case map[string]any:
		return t
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return nil
		}
		var parsed any
		if err := json.Unmarshal([]byte(s), &parsed); err == nil {
			if m, ok := parsed.(map[string]any); ok {
				return m
			}
		}
	}
	return nil
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// isStateVersion2 reports whether a legacy stateVersion marks a V2 record. It
// accepts both the string "2" the legacy writer emits and a numeric 2, since
// JSON numbers decode to float64 through the untyped map.
func isStateVersion2(v any) bool {
	switch t := v.(type) {
	case string:
		return t == "2"
	case float64:
		return t == 2
	}
	return false
}

// legacyLifecycle is the decoded session/runtime halves of the V2 lifecycle.
type legacyLifecycle struct {
	session map[string]any
	runtime map[string]any
}

// extractLifecycle pulls the lifecycle, double-decoding stringified nested
// fields. It prefers the V2 "lifecycle" key, falling back to "statePayload"
// when stateVersion == "2" (mirrors parseLifecycleField).
func extractLifecycle(raw map[string]any) (legacyLifecycle, bool) {
	lc := asObject(raw["lifecycle"])
	if lc == nil && isStateVersion2(raw["stateVersion"]) {
		lc = asObject(raw["statePayload"])
	}
	if lc == nil {
		return legacyLifecycle{}, false
	}
	return legacyLifecycle{
		session: asObject(lc["session"]),
		runtime: asObject(lc["runtime"]),
	}, true
}

// mapActivityState maps the legacy 8-state enum to a rewrite activity_state
// (issue #247 §2.1). Only non-terminal states reach here (terminal orchestrators
// are skipped upstream), so done/terminated need no mapping.
func mapActivityState(state string) domain.ActivityState {
	switch state {
	case "working":
		return domain.ActivityActive
	case "needs_input":
		return domain.ActivityWaitingInput
	default:
		// not_started / idle / detecting / stuck / unknown → idle.
		return domain.ActivityIdle
	}
}

// resumeID picks the rewrite agent_session_id by harness (issue #247 §2.2).
// codex carries codexModel and any harness may carry restoreFallbackReason in
// the legacy record; neither has a rewrite column (the single agent_session_id
// holds only the resume id), so both are dropped — the importer notes them.
func resumeID(harness string, raw map[string]any) string {
	switch harness {
	case "claude-code":
		return asString(raw["claudeSessionUuid"])
	case "codex":
		return asString(raw["codexThreadId"])
	case "opencode":
		return asString(raw["opencodeSessionId"])
	default:
		return ""
	}
}

// mapOrchestratorRecord maps a parsed legacy orchestrator record to a rewrite
// session record. Pure. fileMtime is the last-resort created_at when the record
// carries neither createdAt nor lifecycle.session.startedAt.
func mapOrchestratorRecord(raw map[string]any, projectID, prefix string, fileMtime time.Time) orchestratorMapping {
	base := orchestratorMapping{projectID: projectID, prefix: prefix}

	lc, _ := extractLifecycle(raw)
	state := asString(lc.session["state"])
	_, hasTerminatedAt := lc.session["terminatedAt"]
	terminatedAtNonNull := hasTerminatedAt && lc.session["terminatedAt"] != nil

	// Import ONLY non-terminal, non-terminated orchestrators (gist §6).
	if (state != "" && terminalStates[state]) || terminatedAtNonNull {
		base.status = orchSkipped
		base.note = "orchestrator is terminal (state=" + emptyDash(state) + ")"
		return base
	}

	agent := asString(raw["agent"])
	if !migratableHarnesses[agent] {
		base.status = orchSkipped
		base.note = "harness " + quote(agent) + " is not importable (only claude-code, codex, opencode)"
		return base
	}

	createdAt := firstTime(asString(raw["createdAt"]), asString(lc.session["startedAt"]))
	if createdAt.IsZero() {
		createdAt = fileMtime
	}
	activityLastAt := firstTime(asString(lc.session["lastTransitionAt"]), asString(lc.runtime["lastObservedAt"]))
	if activityLastAt.IsZero() {
		activityLastAt = createdAt
	}
	updatedAt := firstTime(asString(lc.session["lastTransitionAt"]))
	if updatedAt.IsZero() {
		updatedAt = createdAt
	}

	worktree := asString(raw["worktree"])
	rec := domain.SessionRecord{
		ID:          domain.SessionID(prefix + "-orchestrator"),
		ProjectID:   domain.ProjectID(projectID),
		Kind:        domain.KindOrchestrator,
		Harness:     domain.AgentHarness(agent),
		DisplayName: asString(raw["displayName"]),
		Activity: domain.Activity{
			State:          mapActivityState(state),
			LastActivityAt: activityLastAt,
		},
		FirstSignalAt: activityLastAt, // backfill mirrors migration 0010 (#247 §2.1)
		IsTerminated:  false,
		Metadata: domain.SessionMetadata{
			Branch:         asString(raw["branch"]),
			WorkspacePath:  worktree,
			AgentSessionID: resumeID(agent, raw),
			Prompt:         asString(raw["userPrompt"]),
		},
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	}

	base.status = orchMapped
	base.record = rec

	// Note resume metadata the single agent_session_id column cannot hold.
	var dropped []string
	if agent == "codex" {
		if m := asString(raw["codexModel"]); m != "" {
			dropped = append(dropped, "codexModel "+quote(m)+" dropped (no rewrite column; codex resumes by thread id)")
		}
	}
	if r := asString(raw["restoreFallbackReason"]); r != "" {
		dropped = append(dropped, "restoreFallbackReason dropped (forensic only)")
	}
	base.note = strings.Join(dropped, "; ")

	// claude-code orchestrators carry a transcript to relocate (needs both a
	// uuid and a worktree to compute source + destination slugs).
	if agent == "claude-code" {
		if uuid := asString(raw["claudeSessionUuid"]); uuid != "" && worktree != "" {
			base.transcript = &transcriptRelocation{worktree: worktree, uuid: uuid}
		}
	}
	return base
}

// resolveOrchestratorPrefix resolves the import prefix: configured sessionPrefix,
// else the first 12 chars of the project id (matching the rewrite's
// resolvedSessionPrefix and the display-prefix convention).
func resolveOrchestratorPrefix(projectID string, pc legacyProjectConfig) string {
	if p := strings.TrimSpace(pc.SessionPrefix); p != "" {
		return p
	}
	if len(projectID) <= 12 {
		return projectID
	}
	return projectID[:12]
}

// parseJSONRecord parses JSON; nil on invalid/non-object content.
func parseJSONRecord(content string) map[string]any {
	var parsed any
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return nil
	}
	if m, ok := parsed.(map[string]any); ok {
		return m
	}
	return nil
}

// findOrchestratorFile locates a project's orchestrator metadata file: the
// sessions-dir record whose raw role == "orchestrator", else the one named
// "{prefix}-orchestrator.json", else the legacy "orchestrator.json". Skips
// 0-byte and "*.corrupt-*" files (issue #2129 §8.1).
func findOrchestratorFile(sessionsDir, prefix string) string {
	if sessionsDir == "" {
		return ""
	}
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return ""
	}
	var byName string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") || strings.Contains(name, ".corrupt-") {
			continue
		}
		file := filepath.Join(sessionsDir, name)
		content, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		trimmed := strings.TrimSpace(string(content))
		if trimmed == "" {
			continue // 0-byte / reserved id
		}
		raw := parseJSONRecord(trimmed)
		if raw == nil {
			continue
		}
		if asString(raw["role"]) == "orchestrator" {
			return file
		}
		if strings.TrimSuffix(name, ".json") == prefix+"-orchestrator" {
			byName = file
		}
	}
	if byName != "" {
		return byName
	}
	// Defensive: the pre-V2 standalone orchestrator file.
	legacy := filepath.Join(filepath.Dir(sessionsDir), "orchestrator.json")
	if content, err := os.ReadFile(legacy); err == nil && strings.TrimSpace(string(content)) != "" {
		return legacy
	}
	return ""
}

// readOrchestratorMapping reads + maps a project's orchestrator. It returns
// absent when there is no orchestrator file, skipped for terminal/non-importable
// ones, and mapped (with the record and any transcript) otherwise.
func readOrchestratorMapping(sessionsDir, projectID string, pc legacyProjectConfig) orchestratorMapping {
	prefix := resolveOrchestratorPrefix(projectID, pc)
	file := findOrchestratorFile(sessionsDir, prefix)
	if file == "" {
		return orchestratorMapping{projectID: projectID, prefix: prefix, status: orchAbsent}
	}
	content, err := os.ReadFile(file)
	if err != nil {
		return orchestratorMapping{projectID: projectID, prefix: prefix, status: orchAbsent}
	}
	raw := parseJSONRecord(strings.TrimSpace(string(content)))
	if raw == nil {
		return orchestratorMapping{projectID: projectID, prefix: prefix, status: orchAbsent}
	}
	mtime := time.Unix(0, 0).UTC()
	if info, err := os.Stat(file); err == nil {
		mtime = info.ModTime().UTC()
	}
	return mapOrchestratorRecord(raw, projectID, prefix, mtime)
}

// firstTime returns the first RFC3339-parseable timestamp, or zero time.
func firstTime(candidates ...string) time.Time {
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if t, err := time.Parse(time.RFC3339, c); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func emptyDash(s string) string {
	if s == "" {
		return "?"
	}
	return s
}

func quote(s string) string {
	if s == "" {
		return `"?"`
	}
	return `"` + s + `"`
}
