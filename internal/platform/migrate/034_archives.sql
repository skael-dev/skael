-- +goose Up
-- Archives, when the server stores them in Postgres. Each is at most 10 MB.
CREATE TABLE archives (
    name        TEXT PRIMARY KEY,
    data        BYTEA NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The archive store an install started with. An install with no STORAGE_PATH
-- keeps it, so a new default never hides archives written before it.
CREATE TABLE storage_backend (
    id          INT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    kind        TEXT NOT NULL CHECK (kind IN ('filesystem', 'postgres')),
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- An install that already holds an archive did not write it to Postgres.
INSERT INTO storage_backend (kind)
SELECT 'filesystem'
WHERE EXISTS (SELECT 1 FROM skill_versions)
   OR EXISTS (SELECT 1 FROM eval_suites)
   OR EXISTS (SELECT 1 FROM submissions)
   OR EXISTS (SELECT 1 FROM git_pr_heads);

-- +goose Down
DROP TABLE storage_backend;
DROP TABLE archives;
