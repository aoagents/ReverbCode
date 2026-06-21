-- The reviewer agent posts its review to the PR and learns the GitHub review
-- object id (`gh api repos/{owner}/{repo}/pulls/{n}/reviews`). `ao review submit`
-- now carries that id through to the run row so that, when the pass requests
-- changes, AO can tell the worker exactly which GitHub review to address and
-- reply to (issue #337). Empty when the reviewer could not post to the provider.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE review_run ADD COLUMN github_review_id TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE review_run DROP COLUMN github_review_id;
-- +goose StatementEnd
