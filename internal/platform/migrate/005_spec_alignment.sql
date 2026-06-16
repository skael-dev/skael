-- +goose Up
-- Spec alignment: first-class columns for indexed Agent Skills spec fields.
ALTER TABLE skills ADD COLUMN IF NOT EXISTS author TEXT NOT NULL DEFAULT '';
ALTER TABLE skills ADD COLUMN IF NOT EXISTS license TEXT NOT NULL DEFAULT '';
ALTER TABLE skills ADD COLUMN IF NOT EXISTS compatibility TEXT NOT NULL DEFAULT '';
ALTER TABLE skills ADD COLUMN IF NOT EXISTS tags TEXT[] NOT NULL DEFAULT '{}';
ALTER TABLE skills ADD COLUMN IF NOT EXISTS spec_compliance TEXT NOT NULL DEFAULT 'none';

CREATE INDEX IF NOT EXISTS idx_skills_author ON skills (author) WHERE author != '';
CREATE INDEX IF NOT EXISTS idx_skills_tags ON skills USING GIN (tags);

-- Password reset support.
ALTER TABLE users ADD COLUMN IF NOT EXISTS password_reset_required BOOLEAN NOT NULL DEFAULT false;

-- +goose Down
DROP INDEX IF EXISTS idx_skills_tags;
DROP INDEX IF EXISTS idx_skills_author;
ALTER TABLE skills DROP COLUMN IF EXISTS spec_compliance;
ALTER TABLE skills DROP COLUMN IF EXISTS tags;
ALTER TABLE skills DROP COLUMN IF EXISTS compatibility;
ALTER TABLE skills DROP COLUMN IF EXISTS license;
ALTER TABLE skills DROP COLUMN IF EXISTS author;
ALTER TABLE users DROP COLUMN IF EXISTS password_reset_required;
