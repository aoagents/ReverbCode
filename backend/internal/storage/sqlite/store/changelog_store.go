package store

import (
	"context"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// ChangeLogRow is one durable CDC event. These rows are written by the DB
// triggers (migration 0001), never by application code; the store only reads
// them, for the CDC poller.
type ChangeLogRow struct {
	Seq       int64
	ProjectID string
	SessionID string // empty when the event is project-level (NULL in the DB)
	EventType string
	Payload   string
	CreatedAt time.Time
}

// ReadChangeLogAfter returns up to limit events with seq > after, in seq order
// — the CDC poller's read. The frontend's offset is `after`.
func (s *Store) ReadChangeLogAfter(ctx context.Context, after int64, limit int) ([]ChangeLogRow, error) {
	rows, err := s.qr.ReadChangeLogAfter(ctx, gen.ReadChangeLogAfterParams{Seq: after, Limit: int64(limit)})
	if err != nil {
		return nil, fmt.Errorf("read change_log after %d: %w", after, err)
	}
	out := make([]ChangeLogRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, changeLogRowFromGen(r))
	}
	return out, nil
}

// MaxChangeLogSeq returns the highest seq (0 if empty) — a fresh consumer's
// starting offset.
func (s *Store) MaxChangeLogSeq(ctx context.Context) (int64, error) {
	seq, err := s.qr.MaxChangeLogSeq(ctx)
	if err != nil {
		return 0, fmt.Errorf("max change_log seq: %w", err)
	}
	return seq, nil
}

func changeLogRowFromGen(r gen.ChangeLog) ChangeLogRow {
	row := ChangeLogRow{
		Seq:       r.Seq,
		ProjectID: string(r.ProjectID),
		EventType: string(r.EventType),
		Payload:   r.Payload,
		CreatedAt: r.CreatedAt,
	}
	if r.SessionID.Valid {
		row.SessionID = r.SessionID.String
	}
	return row
}
