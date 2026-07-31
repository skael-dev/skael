-- +goose Up
-- Persist the lease duration a worker was granted at claim time, so a
-- heartbeat can re-apply the same lease instead of the server guessing (or
-- worse, silently truncating it to a fixed default on every beat).
ALTER TABLE eval_jobs ADD COLUMN lease_seconds INT NOT NULL DEFAULT 60;

-- +goose Down
ALTER TABLE eval_jobs DROP COLUMN lease_seconds;
