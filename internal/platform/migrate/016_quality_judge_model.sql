-- +goose Up
-- judge_model records which model judged this run. Nullable because every
-- row written before this migration, and any run whose caller could not
-- determine which model served the judge gateway, has none — asReport (in
-- internal/quality/series.go) maps a null here to one shared "unknown judge"
-- value, distinct from any real model name (so a known judge never groups
-- with an unrecorded one) but shared across every unrecorded row, so this
-- deploy does not fragment every skill's pre-existing trend into one-point
-- series.
ALTER TABLE skill_quality ADD COLUMN judge_model TEXT;

-- +goose Down
ALTER TABLE skill_quality DROP COLUMN judge_model;
