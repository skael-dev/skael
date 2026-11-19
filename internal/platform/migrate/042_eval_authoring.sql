-- +goose Up
-- A worker that predates checks would ignore them and score them as passed,
-- so the claim reads this to keep such a suite away from it.
ALTER TABLE eval_suites ADD COLUMN has_checks BOOLEAN NOT NULL DEFAULT FALSE;
-- A suite stored from an open pull request is evaluated in that PR's contest
-- and never becomes the skill's current suite until the merge stores it again.
ALTER TABLE eval_suites ADD COLUMN from_pr BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE git_pr_heads ADD COLUMN suite_ref TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE git_pr_heads DROP COLUMN suite_ref;
ALTER TABLE eval_suites DROP COLUMN from_pr;
ALTER TABLE eval_suites DROP COLUMN has_checks;
