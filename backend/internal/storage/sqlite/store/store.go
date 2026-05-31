// Package store contains SQLite-backed table stores built on sqlc-generated
// queries.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// Store is the SQLite-backed persistence layer. It routes writes to a single
// writer connection (qw) and reads to a reader pool (qr) — see Open. writeMu
// guards the read-modify-write write methods (e.g. CreateSession's
// next-num-then-insert) so concurrent writes can't interleave them.
//
// CDC is captured by DB triggers (migration 0001), NOT by this layer: the store
// never writes change_log, it only reads it for the CDC poller.
type Store struct {
	writeDB *sql.DB
	readDB  *sql.DB
	qw      *gen.Queries // bound to the single writer connection
	qr      *gen.Queries // bound to the reader pool
	writeMu sync.Mutex
}

// NewStore wraps an opened writer + reader *sql.DB (see Open) as a Store.
func NewStore(writeDB, readDB *sql.DB) *Store {
	return &Store{
		writeDB: writeDB,
		readDB:  readDB,
		qw:      gen.New(writeDB),
		qr:      gen.New(readDB),
	}
}

// Close closes both pools.
func (s *Store) Close() error {
	err := s.writeDB.Close()
	if e := s.readDB.Close(); e != nil && err == nil {
		err = e
	}
	return err
}

// inTx runs fn inside a single write transaction on the writer connection,
// rolling back on error. The caller must already hold writeMu.
func (s *Store) inTx(ctx context.Context, what string, fn func(*gen.Queries) error) error {
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin %s: %w", what, err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(s.qw.WithTx(tx)); err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	return tx.Commit()
}
