package main

import (
	"context"
	"log/slog"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/reaper"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// lifecycleStack owns the running LCM + reaper. The LCM is the sole writer of
// canonical transitions; the reaper is the OBSERVE-layer timer that probes live
// runtimes and reports facts back through it.
type lifecycleStack struct {
	LCM        *lifecycle.Manager
	reaperDone <-chan struct{}
}

// startLifecycle constructs the LCM over the store adapter and starts the reaper.
// The goroutine stops when ctx is cancelled; Stop waits for it to drain.
//
// TEMPORARY STUBS (replace as the daemon lane lands the collaborators):
//   - noopNotifier — swap for the notifier multiplexer (desktop/Slack/webhook).
//   - noopMessenger — swap for the runtime/agent-plugin-backed AgentMessenger.
//   - reaper.MapRegistry{} — empty runtime registry, so the reaper ticks
//     escalations but probes nothing until the runtime plugins exist.
func startLifecycle(ctx context.Context, store *sqlite.Store, logger *slog.Logger) (*lifecycleStack, error) {
	a := storeAdapter{store}
	lcm := lifecycle.New(a, a, noopNotifier{}, noopMessenger{})
	rp := reaper.New(lcm, reaper.MapRegistry{}, reaper.Config{Logger: logger})
	return &lifecycleStack{LCM: lcm, reaperDone: rp.Start(ctx)}, nil
}

// Stop waits for the reaper goroutine to exit (the caller must have cancelled the
// ctx passed to startLifecycle).
func (l *lifecycleStack) Stop() { <-l.reaperDone }

// storeAdapter bridges *sqlite.Store to the engine's ports. It embeds the store
// (so CreateSession/UpdateSession/GetSession/ListSessions/ListAllSessions and
// RecentCheckStatuses promote directly) and adds the PR conversions + the
// PRFacts read-model the display status needs.
type storeAdapter struct{ *sqlite.Store }

var (
	_ ports.SessionStore = storeAdapter{}
	_ ports.PRWriter     = storeAdapter{}
)

// PRFactsForSession picks the PR that drives display status — the most-recently
// updated non-closed PR, else the most recent — and folds in whether it has
// unresolved review comments.
func (a storeAdapter) PRFactsForSession(ctx context.Context, id domain.SessionID) (domain.PRFacts, error) {
	rows, err := a.Store.ListPRsBySession(ctx, string(id)) // newest first
	if err != nil {
		return domain.PRFacts{}, err
	}
	if len(rows) == 0 {
		return domain.PRFacts{}, nil
	}
	pick := rows[0]
	for _, r := range rows {
		if r.State == "draft" || r.State == "open" {
			pick = r
			break
		}
	}
	facts := domain.PRFacts{
		URL: pick.URL, Number: int(pick.Number), Exists: true,
		Draft: pick.State == "draft", Merged: pick.State == "merged", Closed: pick.State == "closed",
		CI:           domain.CIState(pick.CIState),
		Review:       domain.ReviewDecision(pick.ReviewDecision),
		Mergeability: domain.Mergeability(pick.Mergeability),
	}
	comments, err := a.Store.ListPRComments(ctx, pick.URL)
	if err != nil {
		return domain.PRFacts{}, err
	}
	for _, c := range comments {
		if !c.Resolved {
			facts.ReviewComments = true
			break
		}
	}
	return facts, nil
}

func (a storeAdapter) WritePR(ctx context.Context, pr ports.PRRow, checks []ports.PRCheckRow, comments []ports.PRComment) error {
	row := sqlite.PRRow{
		URL: pr.URL, SessionID: pr.SessionID, Number: int64(pr.Number),
		State:          prState(pr),
		ReviewDecision: string(pr.Review),
		CIState:        string(pr.CI),
		Mergeability:   string(pr.Mergeability),
		UpdatedAt:      pr.UpdatedAt,
	}
	checkRows := make([]sqlite.PRCheckRow, len(checks))
	for i, c := range checks {
		checkRows[i] = sqlite.PRCheckRow{
			PRURL: c.PRURL, Name: c.Name, CommitHash: c.CommitHash,
			Status: c.Status, URL: c.URL, LogTail: c.LogTail, CreatedAt: c.CreatedAt,
		}
	}
	commentRows := make([]sqlite.PRCommentRow, len(comments))
	for i, c := range comments {
		commentRows[i] = sqlite.PRCommentRow{
			PRURL: pr.URL, CommentID: c.ID, Author: c.Author, File: c.File,
			Line: int64(c.Line), Body: c.Body, Resolved: c.Resolved, CreatedAt: c.CreatedAt,
		}
	}
	return a.Store.WritePRObservation(ctx, row, checkRows, commentRows)
}

// prState collapses the PR's bools into the single pr.state column value.
func prState(r ports.PRRow) string {
	switch {
	case r.Merged:
		return "merged"
	case r.Closed:
		return "closed"
	case r.Draft:
		return "draft"
	default:
		return "open"
	}
}

// noopNotifier / noopMessenger are TEMPORARY stubs (see startLifecycle): the
// write path and CDC work without them; only the human push / agent nudge are
// absent until the real plugins are wired.
type noopNotifier struct{}

func (noopNotifier) Notify(context.Context, ports.Event) error { return nil }

type noopMessenger struct{}

func (noopMessenger) Send(context.Context, domain.SessionID, string) error { return nil }
