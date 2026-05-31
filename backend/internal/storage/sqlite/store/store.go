// Package store contains SQLite-backed table stores built on sqlc-generated
// queries.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
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

// ---- sessions ----

// CreateSession assigns the per-project identity ("{project}-{num}") and inserts
// the record, returning it with ID populated. The next-num read and the insert
// run on the writer connection under writeMu, so two concurrent creates in the
// same project can't collide on num.
func (s *Store) CreateSession(ctx context.Context, rec domain.SessionRecord) (domain.SessionRecord, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	num, err := s.qw.NextSessionNum(ctx, string(rec.ProjectID))
	if err != nil {
		return domain.SessionRecord{}, fmt.Errorf("next session num for %s: %w", rec.ProjectID, err)
	}
	rec.ID = domain.SessionID(fmt.Sprintf("%s-%d", rec.ProjectID, num))
	if err := s.qw.InsertSession(ctx, recordToInsert(rec, num)); err != nil {
		return domain.SessionRecord{}, fmt.Errorf("insert session %s: %w", rec.ID, err)
	}
	return rec, nil
}

// UpdateSession writes the full mutable state of an existing session. The
// id/project/num/created_at are immutable and not touched here.
func (s *Store) UpdateSession(ctx context.Context, rec domain.SessionRecord) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.qw.UpdateSession(ctx, recordToUpdate(rec))
}

// GetSession returns the full record for a session, or ok=false if absent.
func (s *Store) GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	row, err := s.qr.GetSession(ctx, string(id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.SessionRecord{}, false, nil
	}
	if err != nil {
		return domain.SessionRecord{}, false, fmt.Errorf("get session %s: %w", id, err)
	}
	return rowToRecord(row), true, nil
}

// ListSessions returns every session in a project, ordered by num.
func (s *Store) ListSessions(ctx context.Context, project domain.ProjectID) ([]domain.SessionRecord, error) {
	rows, err := s.qr.ListSessionsByProject(ctx, string(project))
	if err != nil {
		return nil, fmt.Errorf("list sessions for %s: %w", project, err)
	}
	return mapSessionRows(rows), nil
}

// ListAllSessions returns every session across all projects.
func (s *Store) ListAllSessions(ctx context.Context) ([]domain.SessionRecord, error) {
	rows, err := s.qr.ListAllSessions(ctx)
	if err != nil {
		return nil, fmt.Errorf("list all sessions: %w", err)
	}
	return mapSessionRows(rows), nil
}

// DeleteSession removes a session (cascades to its pr/checks/comments).
func (s *Store) DeleteSession(ctx context.Context, id domain.SessionID) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.qw.DeleteSession(ctx, string(id))
}

func mapSessionRows(rows []gen.Session) []domain.SessionRecord {
	out := make([]domain.SessionRecord, 0, len(rows))
	for _, r := range rows {
		out = append(out, rowToRecord(r))
	}
	return out
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
