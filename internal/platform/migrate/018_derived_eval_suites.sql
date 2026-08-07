-- +goose Up
-- A suite generated from a skill's own SKILL.md grades the skill against its
-- own claims. That is a usable quality signal and an unusable security gate,
-- so the distinction is persisted rather than inferred: internal/skill's
-- Releaser refuses to clear a scan hold on a derived score.
ALTER TABLE eval_suites ADD COLUMN origin TEXT NOT NULL DEFAULT 'authored';

-- Denormalised from the suite so the UI can label a score without joining.
ALTER TABLE skill_quality ADD COLUMN suite_derived BOOLEAN NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE skill_quality DROP COLUMN suite_derived;
ALTER TABLE eval_suites DROP COLUMN origin;
