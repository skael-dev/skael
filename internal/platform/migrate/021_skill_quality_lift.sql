-- +goose Up
-- Lift is the primary member's own effectiveness minus that same member's
-- baseline. Reported, never gated: QUALITY_FLOOR reads headline_score alone.
--
-- Nullable, because NULL means not measured. A tier that runs no baseline and a
-- skill that did not help are different results.
ALTER TABLE skill_quality ADD COLUMN primary_score REAL;
ALTER TABLE skill_quality ADD COLUMN baseline REAL;
ALTER TABLE skill_quality ADD COLUMN lift REAL;

-- Existing rows stay NULL. Recomputing one from report_json means re-deriving
-- which sessions the original run counted — a second copy of the scoring rule,
-- in a migration, producing numbers a reader takes for measurements.

-- uplift_source has been written as '' on every row since migration 010. It was
-- the placeholder for this number's provenance.
COMMENT ON COLUMN skill_quality.uplift_source IS 'How the baseline was obtained: fresh, reused, or empty when no baseline ran.';

-- +goose Down
COMMENT ON COLUMN skill_quality.uplift_source IS NULL;
ALTER TABLE skill_quality DROP COLUMN lift;
ALTER TABLE skill_quality DROP COLUMN baseline;
ALTER TABLE skill_quality DROP COLUMN primary_score;
