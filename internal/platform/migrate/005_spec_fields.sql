-- +goose Up
ALTER TABLE skills
    ADD COLUMN author         TEXT NOT NULL DEFAULT '',
    ADD COLUMN license        TEXT NOT NULL DEFAULT '',
    ADD COLUMN compatibility  TEXT NOT NULL DEFAULT '',
    ADD COLUMN tags           TEXT[] NOT NULL DEFAULT '{}',
    ADD COLUMN spec_compliance TEXT NOT NULL DEFAULT '';

-- Rebuild the generated search_vector to include author (weight B) and tags (weight C).
ALTER TABLE skills DROP COLUMN search_vector;
ALTER TABLE skills ADD COLUMN search_vector TSVECTOR GENERATED ALWAYS AS (
    setweight(to_tsvector('english', coalesce(name, '')), 'A') ||
    setweight(to_tsvector('english', coalesce(display_name, '')), 'A') ||
    setweight(to_tsvector('english', coalesce(author, '')), 'B') ||
    setweight(to_tsvector('english', coalesce(description, '')), 'B') ||
    setweight(to_tsvector('english', coalesce(array_to_string(tags, ' '), '')), 'C') ||
    setweight(to_tsvector('english', coalesce(content, '')), 'C')
) STORED;

CREATE INDEX idx_skills_search ON skills USING gin(search_vector);
CREATE INDEX idx_skills_tags ON skills USING gin(tags);

-- +goose Down
DROP INDEX IF EXISTS idx_skills_tags;
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
