-- name: InsertChangeLog :one
-- Appends a canonical-write record and returns its monotonic seq so the same
-- transaction can thread it into the outbox row.
INSERT INTO change_log (session_id, event_type, revision, payload, created_at)
VALUES (?, ?, ?, ?, ?)
RETURNING seq;

-- name: InsertOutbox :exec
INSERT INTO outbox (change_log_seq, created_at)
VALUES (?, ?);

-- name: ListUnsentOutbox :many
SELECT o.id, o.change_log_seq, o.attempts,
       c.session_id, c.event_type, c.revision, c.payload, c.created_at
FROM outbox o
JOIN change_log c ON c.seq = o.change_log_seq
WHERE o.sent = 0
ORDER BY o.change_log_seq
LIMIT ?;

-- name: MarkOutboxSent :exec
UPDATE outbox SET sent = 1, sent_at = ? WHERE id = ?;

-- name: MarkOutboxFailed :exec
UPDATE outbox SET attempts = attempts + 1, last_error = ? WHERE id = ?;

-- name: GetConsumerOffset :one
SELECT last_seq FROM consumer_offsets WHERE consumer = ?;

-- name: UpsertConsumerOffset :exec
INSERT INTO consumer_offsets (consumer, last_seq, updated_at)
VALUES (?, ?, ?)
ON CONFLICT (consumer) DO UPDATE SET last_seq = excluded.last_seq, updated_at = excluded.updated_at;

-- name: MaxChangeLogSeq :one
SELECT CAST(COALESCE(MAX(seq), 0) AS INTEGER) FROM change_log;

-- name: MinConsumerOffset :one
SELECT CAST(COALESCE(MIN(last_seq), 0) AS INTEGER) FROM consumer_offsets;

-- name: DeleteSentOutboxBelow :execrows
-- Sweeps outbox rows that every consumer has acknowledged. The bound is the
-- MIN(consumer_offsets.last_seq), which is the highest seq each consumer has
-- already delivered — so the row at that seq is itself fully acknowledged and
-- safe to delete. Use <= so a quiet consumer eventually reaches a fully
-- vacuumed outbox; a strict < would always leave one stale row behind.
DELETE FROM outbox WHERE sent = 1 AND change_log_seq <= ?;
