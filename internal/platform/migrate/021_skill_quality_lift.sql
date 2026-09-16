-- +goose Up
-- Lift is the primary member's own effectiveness minus that same member's
-- baseline. It is reported, never gated: QUALITY_FLOOR still reads
-- headline_score alone.
--
-- All three are nullable, and NULL means not measured. A tier that runs no
-- baseline, and a run whose primary member was unhealthy, both produce no
-- comparison at all — which is a different fact from a lift of zero.
ALTER TABLE skill_quality ADD COLUMN primary_score REAL;
ALTER TABLE skill_quality ADD COLUMN baseline REAL;
ALTER TABLE skill_quality ADD COLUMN lift REAL;

-- Existing rows stay NULL. report_json holds enough to recompute a lift, but
-- doing so means re-deriving which sessions the original run counted (void
-- tasks excluded, dropped grades removed from the denominator) — a second copy
-- of the scoring rule, living in a migration, producing numbers a reader takes
-- for measurements. A gap before this release is honest; a wrong number is not.

-- uplift_source has been written as '' on every row since migration 010. It was
-- the placeholder for this number's provenance: 'fresh' or 'reused', from
-- report.ReusedBaselines. A reader comparing two lifts needs to know when one
-- side came from a run copied out of an earlier eval.
COMMENT ON COLUMN skill_quality.uplift_source IS 'How the baseline was obtained: fresh, reused, or empty when no baseline ran.';

-- +goose Down
COMMENT ON COLUMN skill_quality.uplift_source IS NULL;
ALTER TABLE skill_quality DROP COLUMN lift;
ALTER TABLE skill_quality DROP COLUMN baseline;
ALTER TABLE skill_quality DROP COLUMN primary_score;
