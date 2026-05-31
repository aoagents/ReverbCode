package main

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/aoagents/agent-orchestrator/backend/internal/cdc"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// cdcPipeline owns the running CDC poller and the broadcaster the SSE transport
// subscribes to. The DB triggers write change_log; the poller tails it and fans
// each new event out through the broadcaster. Durable catch-up is the client's
// job (it reads change_log from its own Last-Event-ID), so the poller only
// pushes live events and re-seeks to head on restart.
type cdcPipeline struct {
	Broadcaster *cdc.Broadcaster
	done        <-chan struct{}
}

// startCDC seeks the poller to the current head and starts its loop. It stops
// when ctx is cancelled; Stop waits for it to drain.
func startCDC(ctx context.Context, store *sqlite.Store, logger *slog.Logger) (*cdcPipeline, error) {
	bcast := cdc.NewBroadcaster()
	poller := cdc.NewPoller(cdcSource{store}, bcast, cdc.PollerConfig{Logger: logger})
	if err := poller.SeekToHead(ctx); err != nil {
		return nil, err
	}
	return &cdcPipeline{Broadcaster: bcast, done: poller.Start(ctx)}, nil
}

// Stop waits for the poller goroutine to exit (the caller must have cancelled the
// ctx passed to startCDC).
func (p *cdcPipeline) Stop() error {
	<-p.done
	return nil
}

// cdcSource adapts *sqlite.Store's change_log reads to cdc.Source.
type cdcSource struct{ store *sqlite.Store }

func (s cdcSource) EventsAfter(ctx context.Context, after int64, limit int) ([]cdc.Event, error) {
	rows, err := s.store.ReadChangeLogAfter(ctx, after, limit)
	if err != nil {
		return nil, err
	}
	out := make([]cdc.Event, len(rows))
	for i, r := range rows {
		out[i] = cdc.Event{
			Seq:       r.Seq,
			ProjectID: r.ProjectID,
			SessionID: r.SessionID,
			Type:      cdc.EventType(r.EventType),
			Payload:   json.RawMessage(r.Payload),
			CreatedAt: r.CreatedAt,
		}
	}
	return out, nil
}

func (s cdcSource) LatestSeq(ctx context.Context) (int64, error) {
	return s.store.MaxChangeLogSeq(ctx)
}
