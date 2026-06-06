package notification

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func fingerprint(intent domain.NotificationIntent, facts EnrichedFacts, actions []domain.NotificationAction, content domain.NotificationContent) (string, error) {
	type actionFingerprint struct {
		ID      string `json:"id"`
		URL     string `json:"url,omitempty"`
		Route   string `json:"route,omitempty"`
		Primary bool   `json:"primary,omitempty"`
	}
	actionFP := make([]actionFingerprint, 0, len(actions))
	for _, a := range actions {
		actionFP = append(actionFP, actionFingerprint{ID: a.ID, URL: a.URL, Route: a.Route, Primary: a.Primary})
	}
	sort.Slice(actionFP, func(i, j int) bool { return actionFP[i].ID < actionFP[j].ID })

	failed := make([]map[string]string, 0, len(facts.FailedChecks))
	for _, c := range facts.FailedChecks {
		failed = append(failed, map[string]string{"name": c.Name, "commit": c.CommitHash, "status": string(c.Status), "url": c.URL})
	}
	sort.Slice(failed, func(i, j int) bool {
		if failed[i]["name"] == failed[j]["name"] {
			return failed[i]["commit"] < failed[j]["commit"]
		}
		return failed[i]["name"] < failed[j]["name"]
	})

	var pr map[string]any
	if facts.PR != nil {
		pr = map[string]any{
			"url":            facts.PR.URL,
			"headSha":        facts.PR.HeadSHA,
			"baseSha":        facts.PR.BaseSHA,
			"mergeCommitSha": facts.PR.MergeCommitSHA,
			"ci":             facts.PR.CI,
			"review":         facts.PR.Review,
			"mergeability":   facts.PR.Mergeability,
		}
	} else if facts.PRURL != "" {
		pr = map[string]any{"url": facts.PRURL}
	}
	input := map[string]any{
		"type":       intent.Type,
		"priority":   intent.Priority,
		"title":      content.Title,
		"summary":    content.Summary,
		"actions":    actionFP,
		"pr":         pr,
		"checkName":  intent.Context.CheckName,
		"checkURL":   intent.Context.CheckURL,
		"commitHash": intent.Context.CommitHash,
		"reviewIDs":  sortedCopy(intent.Context.ReviewIDs),
		"threadIDs":  sortedCopy(intent.Context.ThreadIDs),
		"mergeState": intent.Context.MergeState,
		"reason":     intent.Context.Reason,
		"failed":     failed,
	}
	b, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
