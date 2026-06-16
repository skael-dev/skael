-- +goose Up
-- Add Agent Skills spec metadata columns.
ALTER TABLE skills
    ADD COLUMN author         TEXT NOT NULL DEFAULT '',
    ADD COLUMN license        TEXT NOT NULL DEFAULT '',
    ADD COLUMN compatibility  TEXT NOT NULL DEFAULT '',
    ADD COLUMN tags           TEXT[] NOT NULL DEFAULT '{}',
    ADD COLUMN spec_compliance TEXT NOT NULL DEFAULT '';

-- Rebuild the generated search_vector to include author (weight B).
-- Note: tags are indexed separately via GIN and filtered via WHERE clauses;
-- array_to_string is not immutable so it cannot appear in a generated column.
ALTER TABLE skills DROP COLUMN search_vector;
ALTER TABLE skills ADD COLUMN search_vector TSVECTOR GENERATED ALWAYS AS (
    setweight(to_tsvector('english', coalesce(name, '')), 'A') ||
    setweight(to_tsvector('english', coalesce(display_name, '')), 'A') ||
    setweight(to_tsvector('english', coalesce(author, '')), 'B') ||
    setweight(to_tsvector('english', coalesce(description, '')), 'B') ||
    setweight(to_tsvector('english', coalesce(content, '')), 'C')
) STORED;

CREATE INDEX idx_skills_search ON skills USING gin(search_vector);
CREATE INDEX idx_skills_author ON skills (author) WHERE author != '';
CREATE INDEX idx_skills_tags ON skills USING gin(tags);

-- Password reset support.
ALTER TABLE users ADD COLUMN password_reset_required BOOLEAN NOT NULL DEFAULT false;

-- +goose Down
DROP INDEX IF EXISTS idx_skills_tags;
DROP INDEX IF EXISTS idx_skills_author;
DROP INDEX IF EXISTS idx_skills_search;

ALTER TABLE skills DROP COLUMN search_vector;
ALTER TABLE skills ADD COLUMN search_vector TSVECTOR GENERATED ALWAYS AS (
    setweight(to_tsvector('english', coalesce(name, '')), 'A') ||
    setweight(to_tsvector('english', coalesce(display_name, '')), 'A') ||
    setweight(to_tsvector('english', coalesce(description, '')), 'B') ||
    setweight(to_tsvector('english', coalesce(content, '')), 'C')
) STORED;

CREATE INDEX idx_skills_search ON skills USING gin(search_vector);

ALTER TABLE skills
    DROP COLUMN author,
    DROP COLUMN license,
    DROP COLUMN compatibility,
    DROP COLUMN tags,
    DROP COLUMN spec_compliance;

ALTER TABLE users DROP COLUMN password_reset_required;
