-- +goose Up
-- origin now answers one question for two different suites. A worker
-- generated this one. A person pushed that one and nobody has read it. Both
-- are 'derived', because neither can release a held version.
--
-- Void tolerance needs them apart. A worker-generated suite is gated by its
-- own oracles with nobody to repair a task that fails, which is why it asks
-- for 18 tasks rather than 10 and why its run excludes the void ones. An
-- unread push has a present, named author who can go and repair them, so it
-- keeps the stricter contract. See worker.RunInput.AllowVoid.
ALTER TABLE eval_suites ADD COLUMN machine_generated BOOLEAN NOT NULL DEFAULT false;

-- Every row already recorded derived was recorded so by a worker's own push.
-- The unreviewed push arrives in the same release as this column, so nothing
-- else can have written that origin yet.
UPDATE eval_suites SET machine_generated = true WHERE origin = 'derived';

-- +goose Down
ALTER TABLE eval_suites DROP COLUMN machine_generated;
