-- +goose Up
CREATE TABLE import_sources (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    skill_id     UUID NOT NULL UNIQUE REFERENCES skills(id) ON DELETE CASCADE,
    source_type  TEXT NOT NULL,
    source_url   TEXT NOT NULL DEFAULT '',
    source_path  TEXT NOT NULL DEFAULT '',
    source_ref   TEXT NOT NULL DEFAULT '',
    commit_sha   TEXT NOT NULL DEFAULT '',
    imported_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_checked TIMESTAMPTZ
);

CREATE INDEX idx_import_sources_skill_id ON import_sources(skill_id);

-- +goose Down
DROP TABLE IF EXISTS import_sources;
