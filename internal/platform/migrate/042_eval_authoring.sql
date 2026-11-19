-- +goose Up
-- A worker that predates checks would ignore them and score them as passed,
-- so the claim reads this to keep such a suite away from it.
ALTER TABLE eval_suites ADD COLUMN has_checks BOOLEAN NOT NULL DEFAULT FALSE;
-- A suite stored from an open pull request is evaluated in that PR's contest
-- and never becomes the skill's current suite until the merge stores it again.
ALTER TABLE eval_suites ADD COLUMN from_pr BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE git_pr_heads ADD COLUMN suite_ref TEXT NOT NULL DEFAULT '';
-- A note is a folder that synced with something to report, such as a suite
-- too small for any tier, so no eval was queued.
ALTER TABLE git_connection_refusals ADD COLUMN kind TEXT NOT NULL DEFAULT 'refused'
    CHECK (kind IN ('refused', 'note'));

-- +goose Down
DELETE FROM git_connection_refusals WHERE kind = 'note';
ALTER TABLE git_connection_refusals DROP COLUMN kind;
ALTER TABLE git_pr_heads DROP COLUMN suite_ref;
ALTER TABLE eval_suites DROP COLUMN from_pr;
ALTER TABLE eval_suites DROP COLUMN has_checks;
