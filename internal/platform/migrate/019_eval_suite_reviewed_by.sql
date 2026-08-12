-- +goose Up
-- uploaded_by names whoever pushed the suite, which after a review is the
-- wrong person. A review raises the suite to authored, and an authored suite
-- is the one whose score can release a held version, so the reviewer is the
-- accountable name. Empty means nobody has reviewed this suite yet.
ALTER TABLE eval_suites ADD COLUMN reviewed_by TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE eval_suites DROP COLUMN reviewed_by;
