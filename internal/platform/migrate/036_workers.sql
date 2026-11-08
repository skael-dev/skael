-- +goose Up
-- One row per worker, written by its check-in on every poll. It holds the
-- names of the credentials a worker has, never their values.
CREATE TABLE workers (
    id                  TEXT PRIMARY KEY,
    version             TEXT NOT NULL DEFAULT '',
    driver              TEXT NOT NULL DEFAULT '',
    hardware_isolated   BOOLEAN NOT NULL DEFAULT false,
    network_policy      BOOLEAN NOT NULL DEFAULT false,
    concurrency         INT NOT NULL DEFAULT 1,
    sandbox_image       TEXT NOT NULL DEFAULT '',
    pinned              TEXT NOT NULL DEFAULT '',
    credentials         JSONB NOT NULL DEFAULT '[]',
    verifications       JSONB NOT NULL DEFAULT '[]',
    first_seen          TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen           TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE workers;
