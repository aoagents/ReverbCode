package cdc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"
)

// DefaultPollInterval is how often the consumer checks the log for new bytes.
// Polling (rather than fs-notify) keeps the consumer dependency-free; at this
// cadence live updates stay well under a human-perceptible delay.
const DefaultPollInterval = 100 * time.Millisecond

// OffsetStore persists the consumer's durable seq cursor (at-least-once).
type OffsetStore interface {
	GetOffset(ctx context.Context, consumer string) (int64, error)
	SetOffset(ctx context.Context, consumer string, seq int64, at time.Time) error
}

// SnapshotSource rebuilds current state from the source of truth (the sessions
// table) after a rotation gap, where log lines for unconsumed-but-already-sent
// events were truncated away. It returns one Event per live session plus the
// MAX(change_log seq) the snapshot corresponds to, so the consumer can resume.
type SnapshotSource interface {
	Snapshot(ctx context.Context) (events []Event, maxSeq int64, err error)
}

// Consumer tails the JSONL log, deduplicates by seq, and fans each new event
// out through the Broadcaster, persisting its durable offset as it goes.
type Consumer struct {
	name     string
	path     string
	offsets  OffsetStore
	bcast    *Broadcaster
	snapshot SnapshotSource
	interval time.Duration
	clock    func() time.Time
	logger   *slog.Logger

	cursor   int64       // byte offset into the log
	lastSeq  int64       // highest seq delivered
	prevInfo os.FileInfo // identity of the file last polled (rotation detection)
}

// ConsumerConfig holds optional knobs and the snapshot source.
type ConsumerConfig struct {
	Snapshot SnapshotSource
	Interval time.Duration
	Clock    func() time.Time
	Logger   *slog.Logger
}

// NewConsumer constructs a Consumer named name (the consumer_offsets key) over
// the log at path, fanning out through bcast and persisting offsets via offsets.
func NewConsumer(name, path string, offsets OffsetStore, bcast *Broadcaster, cfg ConsumerConfig) *Consumer {
	c := &Consumer{
		name:     name,
		path:     path,
		offsets:  offsets,
		bcast:    bcast,
		snapshot: cfg.Snapshot,
		interval: cfg.Interval,
		clock:    cfg.Clock,
		logger:   cfg.Logger,
	}
	if c.interval <= 0 {
		c.interval = DefaultPollInterval
	}
	if c.clock == nil {
		c.clock = time.Now
	}
	if c.logger == nil {
		c.logger = slog.Default()
	}
	return c
}

// Start loads the durable offset and runs the poll loop until ctx is cancelled;
// the returned channel closes when the loop has exited.
//
// On startup, if a prior offset was loaded (lastSeq > 0), Start resyncs from
// the SnapshotSource (if wired) before entering the poll loop. The first Poll
// cannot detect a rotation that happened while the consumer was down (prevInfo
// is nil, cursor is 0, so neither the os.SameFile nor the size-shrunk branch
// fires), which would otherwise silently drop every event in the rotated-out
// archive between lastSeq+1 and the rotation point. A fresh start (lastSeq=0)
// has nothing to miss — every persisted event will still be in the live JSONL
// or in change_log — so the resync is skipped to avoid the extra DB roundtrip
// shifting the publisher/consumer cadence the E2E tests depend on.
func (c *Consumer) Start(ctx context.Context) (<-chan struct{}, error) {
	seq, err := c.offsets.GetOffset(ctx, c.name)
	if err != nil {
		return nil, fmt.Errorf("load consumer offset: %w", err)
	}
	c.lastSeq = seq

	if c.snapshot != nil && c.lastSeq > 0 {
		if err := c.resync(ctx); err != nil {
			return nil, err
		}
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(c.interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := c.Poll(ctx); err != nil {
					c.logger.Error("cdc consumer: poll failed", "err", err)
				}
			}
		}
	}()
	return done, nil
}

// Poll reads any new bytes since the last cursor and delivers complete lines. It
// detects rotation (the file shrank below the cursor) and resyncs from the DB
// snapshot before resuming.
func (c *Consumer) Poll(ctx context.Context) error {
	f, err := os.Open(c.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // publisher has not created the log yet
		}
		return fmt.Errorf("open cdc log: %w", err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat cdc log: %w", err)
	}
	size := info.Size()

	rotated := (c.prevInfo != nil && !os.SameFile(c.prevInfo, info)) || size < c.cursor
	c.prevInfo = info
	if rotated {
		// The previous file's bytes are void. Resync from the DB snapshot (if
		// wired), then resume reading the fresh file from the top.
		if err := c.resync(ctx); err != nil {
			return err
		}
		c.cursor = 0
	}
	if size == c.cursor {
		return nil
	}

	if _, err := f.Seek(c.cursor, io.SeekStart); err != nil {
		return fmt.Errorf("seek cdc log: %w", err)
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return fmt.Errorf("read cdc log: %w", err)
	}

	consumed, maxSeq := c.processLines(data)
	c.cursor += int64(consumed)

	if maxSeq > c.lastSeq {
		c.lastSeq = maxSeq
		if err := c.offsets.SetOffset(ctx, c.name, c.lastSeq, c.clock().UTC()); err != nil {
			return fmt.Errorf("persist consumer offset: %w", err)
		}
	}
	return nil
}

// processLines delivers each complete (newline-terminated) line, skipping reset
// markers and any event whose seq was already delivered. It returns the number
// of bytes consumed (only complete lines) and the highest seq seen.
func (c *Consumer) processLines(data []byte) (consumed int, maxSeq int64) {
	maxSeq = c.lastSeq
	for {
		nl := bytes.IndexByte(data[consumed:], '\n')
		if nl < 0 {
			return consumed, maxSeq // partial trailing line: leave for next poll
		}
		line := data[consumed : consumed+nl]
		consumed += nl + 1

		if isResetMarker(line) {
			continue
		}
		var e Event
		if err := json.Unmarshal(line, &e); err != nil {
			c.logger.Error("cdc consumer: bad line skipped", "err", err)
			continue
		}
		if e.Seq <= c.lastSeq {
			continue // idempotent: already delivered
		}
		c.bcast.Publish(e)
		if e.Seq > maxSeq {
			maxSeq = e.Seq
		}
	}
}

func (c *Consumer) resync(ctx context.Context) error {
	if c.snapshot == nil {
		return nil
	}
	events, maxSeq, err := c.snapshot.Snapshot(ctx)
	if err != nil {
		return fmt.Errorf("cdc consumer resync: %w", err)
	}
	for _, e := range events {
		c.bcast.Publish(e)
	}
	if maxSeq > c.lastSeq {
		c.lastSeq = maxSeq
		if err := c.offsets.SetOffset(ctx, c.name, c.lastSeq, c.clock().UTC()); err != nil {
			return fmt.Errorf("persist offset after resync: %w", err)
		}
	}
	return nil
}

func isResetMarker(line []byte) bool {
	var m resetMarker
	if err := json.Unmarshal(line, &m); err != nil {
		return false
	}
	return m.Type == "reset"
}
