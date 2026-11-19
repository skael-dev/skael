-- +goose Up
-- A rescan withdraws a released version that has an unappealable finding.
ALTER TABLE skill_versions
    DROP CONSTRAINT skill_versions_gate_state_check;
ALTER TABLE skill_versions
    ADD CONSTRAINT skill_versions_gate_state_check
    CHECK (gate_state IN ('released', 'needs_review', 'rejected', 'withdrawn'));

-- The first rescan of a released version copies scan_result here, so a finding
-- new since release stays visible after later rescans.
ALTER TABLE skill_versions
    ADD COLUMN release_scan_result JSONB,
    ADD COLUMN rescanned_at        TIMESTAMPTZ;

-- +goose Down
UPDATE skill_versions SET gate_state = 'rejected' WHERE gate_state = 'withdrawn';
ALTER TABLE skill_versions
    DROP CONSTRAINT skill_versions_gate_state_check;
ALTER TABLE skill_versions
    ADD CONSTRAINT skill_versions_gate_state_check
    CHECK (gate_state IN ('released', 'needs_review', 'rejected'));
ALTER TABLE skill_versions
    DROP COLUMN release_scan_result,
    DROP COLUMN rescanned_at;
