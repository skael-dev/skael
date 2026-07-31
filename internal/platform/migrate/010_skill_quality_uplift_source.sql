-- +goose Up
-- report.Comparable treats UpliftSource as one of the facts that determines
-- whether two reports' scores are a fair comparison, alongside SuiteRef,
-- EngineVersion, Tier, ModelPanel, and PanelComplete — all of which
-- skill_quality already carries. Without this column a version-over-version
-- trend cannot tell whether its own comparison is valid without re-fetching
-- every full report, which defeats the point of the summary table.
ALTER TABLE skill_quality ADD COLUMN uplift_source TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE skill_quality DROP COLUMN uplift_source;
