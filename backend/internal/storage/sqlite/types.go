package sqlite

import sqlitestore "github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"

// Store is the SQLite-backed persistence layer.
type Store = sqlitestore.Store

// ChangeLogRow is one durable CDC event row.
type ChangeLogRow = sqlitestore.ChangeLogRow
