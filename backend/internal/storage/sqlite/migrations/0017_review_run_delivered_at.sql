-- AO-internal review changes-requested nudges are delivered through lifecycle
-- sendOnce after the review result is recorded. This nullable timestamp marks
-- passes whose worker nudge was durably delivered, so retries after a daemon
-- restart do not send the same review pass twice.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE review_run ADD COLUMN delivered_at TIMESTAMP;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE review_run DROP COLUMN delivered_at;
-- +goose StatementEnd
