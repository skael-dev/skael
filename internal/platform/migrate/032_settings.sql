-- +goose Up
-- One row per setting changed in the UI. A secret holds an env: or file:
-- reference in value, or a literal sealed under the master key in sealed.
CREATE TABLE settings (
    key         TEXT PRIMARY KEY,
    value       TEXT NOT NULL DEFAULT '',
    sealed      TEXT NOT NULL DEFAULT '',
    updated_by  TEXT NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Every save bumps the version, so a replica that missed a NOTIFY sees the
-- change on its next poll.
CREATE TABLE settings_version (
    id      INT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    version BIGINT NOT NULL
);
INSERT INTO settings_version (id, version) VALUES (1, 0);

-- +goose Down
DROP TABLE settings_version;
DROP TABLE settings;
