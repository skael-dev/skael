-- +goose Up
CREATE TABLE skill_aliases (
    alias      TEXT PRIMARY KEY,
    canonical  TEXT NOT NULL REFERENCES skills(name) ON UPDATE CASCADE ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_skill_aliases_canonical ON skill_aliases(canonical);

-- +goose Down
DROP TABLE IF EXISTS skill_aliases;
