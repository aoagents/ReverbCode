-- +goose Up
-- Repair any existing TIMESTAMP values that were serialized by Go's
-- time.Time.String() with an unnamed fixed-offset Location (for example,
-- "2026-10-17 11:19:16.938849 -0400 -0400" when the daemon's cached local zone
-- differed from a hook timestamp's numeric offset). Rewriting the trailing
-- numeric zone token to "GMT" makes the string valid under
-- "2006-01-02 15:04:05.999999999 -0700 MST" while preserving the exact instant
-- encoded by the first numeric offset.
-- +goose StatementBegin
UPDATE sessions
SET activity_last_at = substr(activity_last_at, 1, length(activity_last_at) - 5) || 'GMT'
WHERE activity_last_at GLOB '* [+-][0-9][0-9][0-9][0-9] [+-][0-9][0-9][0-9][0-9]';

UPDATE sessions
SET first_signal_at = substr(first_signal_at, 1, length(first_signal_at) - 5) || 'GMT'
WHERE first_signal_at GLOB '* [+-][0-9][0-9][0-9][0-9] [+-][0-9][0-9][0-9][0-9]';

UPDATE sessions
SET created_at = substr(created_at, 1, length(created_at) - 5) || 'GMT'
WHERE created_at GLOB '* [+-][0-9][0-9][0-9][0-9] [+-][0-9][0-9][0-9][0-9]';

UPDATE sessions
SET updated_at = substr(updated_at, 1, length(updated_at) - 5) || 'GMT'
WHERE updated_at GLOB '* [+-][0-9][0-9][0-9][0-9] [+-][0-9][0-9][0-9][0-9]';

UPDATE projects
SET registered_at = substr(registered_at, 1, length(registered_at) - 5) || 'GMT'
WHERE registered_at GLOB '* [+-][0-9][0-9][0-9][0-9] [+-][0-9][0-9][0-9][0-9]';

UPDATE projects
SET archived_at = substr(archived_at, 1, length(archived_at) - 5) || 'GMT'
WHERE archived_at GLOB '* [+-][0-9][0-9][0-9][0-9] [+-][0-9][0-9][0-9][0-9]';

UPDATE pr
SET updated_at = substr(updated_at, 1, length(updated_at) - 5) || 'GMT'
WHERE updated_at GLOB '* [+-][0-9][0-9][0-9][0-9] [+-][0-9][0-9][0-9][0-9]';

UPDATE review
SET created_at = substr(created_at, 1, length(created_at) - 5) || 'GMT'
WHERE created_at GLOB '* [+-][0-9][0-9][0-9][0-9] [+-][0-9][0-9][0-9][0-9]';

UPDATE review
SET updated_at = substr(updated_at, 1, length(updated_at) - 5) || 'GMT'
WHERE updated_at GLOB '* [+-][0-9][0-9][0-9][0-9] [+-][0-9][0-9][0-9][0-9]';

UPDATE review_run
SET created_at = substr(created_at, 1, length(created_at) - 5) || 'GMT'
WHERE created_at GLOB '* [+-][0-9][0-9][0-9][0-9] [+-][0-9][0-9][0-9][0-9]';

UPDATE review_run
SET delivered_at = substr(delivered_at, 1, length(delivered_at) - 5) || 'GMT'
WHERE delivered_at GLOB '* [+-][0-9][0-9][0-9][0-9] [+-][0-9][0-9][0-9][0-9]';

UPDATE notifications
SET created_at = substr(created_at, 1, length(created_at) - 5) || 'GMT'
WHERE created_at GLOB '* [+-][0-9][0-9][0-9][0-9] [+-][0-9][0-9][0-9][0-9]';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
