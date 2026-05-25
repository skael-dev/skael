-- +goose Up
CREATE TABLE dismissed_skills (
    name         TEXT PRIMARY KEY,
    dismissed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS dismissed_skills;
