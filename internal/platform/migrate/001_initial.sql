-- +goose Up
CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE TABLE skills (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            TEXT NOT NULL UNIQUE,
    display_name    TEXT,
    description     TEXT NOT NULL DEFAULT '',
    content         TEXT NOT NULL DEFAULT '',
    search_vector   TSVECTOR GENERATED ALWAYS AS (
        setweight(to_tsvector('english', coalesce(name, '')), 'A') ||
        setweight(to_tsvector('english', coalesce(display_name, '')), 'A') ||
        setweight(to_tsvector('english', coalesce(description, '')), 'B') ||
        setweight(to_tsvector('english', coalesce(content, '')), 'C')
    ) STORED,
    latest_version  INT NOT NULL DEFAULT 0,
    frontmatter     JSONB NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    reviewed_at     TIMESTAMPTZ,
    reviewed_by     TEXT NOT NULL DEFAULT ''
);

CREATE INDEX idx_skills_search ON skills USING gin(search_vector);
CREATE INDEX idx_skills_name_trgm ON skills USING gin(name gin_trgm_ops);

CREATE TABLE skill_versions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    skill_id        UUID NOT NULL REFERENCES skills(id) ON DELETE CASCADE,
    version         INT NOT NULL,
    archive_path    TEXT NOT NULL,
    checksum        TEXT NOT NULL,
    changelog       TEXT NOT NULL DEFAULT '',
    frontmatter     JSONB NOT NULL DEFAULT '{}',
    file_manifest   JSONB NOT NULL DEFAULT '[]',
    scan_result     JSONB NOT NULL DEFAULT '{}',
    published_by    TEXT NOT NULL DEFAULT 'system',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(skill_id, version)
);

CREATE TABLE skill_events (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    skill_name      TEXT NOT NULL,
    agent           TEXT NOT NULL,
    trigger_type    TEXT NOT NULL DEFAULT 'auto',
    project_hash    TEXT NOT NULL DEFAULT '',
    developer_hash  TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_events_skill_time ON skill_events (skill_name, created_at DESC);
CREATE INDEX idx_events_created ON skill_events (created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS skill_events;
DROP TABLE IF EXISTS skill_versions;
DROP TABLE IF EXISTS skills;
DROP EXTENSION IF EXISTS pg_trgm;
