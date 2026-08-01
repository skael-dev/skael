-- +goose Up
-- The publish gate holds a version that trips an appealable scan finding: the
-- row and its archive exist, because the evaluation that may clear it needs a
-- real bundle to run against, but skills.latest_version does not advance.
--
-- The vocabulary here is deliberately "gate", not "review". skills.reviewed_at
-- and skills.reviewed_by already exist and mean something unrelated — a human
-- marking a skill as security-reviewed.
--
-- Every column is plain and additive: adding org_id later stays a bare
-- ALTER TABLE ADD COLUMN with no constraint rebuild.
ALTER TABLE skill_versions
    ADD COLUMN gate_state    TEXT NOT NULL DEFAULT 'released',
    ADD COLUMN gate_decision JSONB NOT NULL DEFAULT '{}',
    ADD COLUMN gated_by      TEXT NOT NULL DEFAULT '',
    ADD COLUMN gated_at      TIMESTAMPTZ,
    ADD COLUMN gate_note     TEXT NOT NULL DEFAULT '';

ALTER TABLE skill_versions
    ADD CONSTRAINT skill_versions_gate_state_check
    CHECK (gate_state IN ('released', 'needs_review', 'rejected'));

-- Held versions are the ones anything ever scans for.
CREATE INDEX idx_skill_versions_gate_state ON skill_versions (gate_state)
    WHERE gate_state <> 'released';

-- Counted in quality.FromReport from report.RunDrift.Violations, which the
-- per-member drift aggregate in drift_breakdown does not preserve. Existing
-- rows default to 0: no Phase 4 report carried the field, and no pre-existing
-- version is held, so nothing consults it.
ALTER TABLE skill_quality
    ADD COLUMN critical_forbid_violations INT NOT NULL DEFAULT 0;

-- +goose Down
DROP INDEX IF EXISTS idx_skill_versions_gate_state;
ALTER TABLE skill_versions
    DROP CONSTRAINT IF EXISTS skill_versions_gate_state_check;
ALTER TABLE skill_versions
    DROP COLUMN IF EXISTS gate_state,
    DROP COLUMN IF EXISTS gate_decision,
    DROP COLUMN IF EXISTS gated_by,
    DROP COLUMN IF EXISTS gated_at,
    DROP COLUMN IF EXISTS gate_note;
ALTER TABLE skill_quality
    DROP COLUMN IF EXISTS critical_forbid_violations;
