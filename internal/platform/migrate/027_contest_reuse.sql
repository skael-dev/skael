-- +goose Up
-- A contest candidate whose report came from another contest's same-day run
-- names that contest. NULL means this contest measured the candidate itself.
ALTER TABLE contest_candidates
    ADD COLUMN reused_from UUID REFERENCES contests(id) ON DELETE SET NULL;

-- The reuse lookup: one candidate's finished reports by version, newest first.
CREATE INDEX idx_contest_candidates_reuse ON contest_candidates(skill_id, version)
    WHERE report_json IS NOT NULL AND reused_from IS NULL;

-- +goose Down
DROP INDEX idx_contest_candidates_reuse;
ALTER TABLE contest_candidates DROP COLUMN reused_from;
