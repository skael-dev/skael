-- +goose Up
-- The published bundle never carries the authored spec.yaml (it is authoring
-- scaffolding, stripped before packing) — so a worker rebuilding a workspace
-- from a downloaded bundle has no way to recover it. Carrying the real spec
-- alongside the suite it was checked against means the worker's sandbox gets
-- the skill's actual deps instead of an empty placeholder, and the judge
-- rubric gets the skill's actual purpose instead of a stand-in string.
-- Nullable: older rows (and any caller that has not started sending it yet)
-- have none, and a worker must be able to tell "not recorded" from "empty".
ALTER TABLE eval_suites ADD COLUMN spec JSONB;

-- +goose Down
ALTER TABLE eval_suites DROP COLUMN spec;
