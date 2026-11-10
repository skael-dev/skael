-- +goose Up
-- A set entry is an exact skill name, a pattern such as "payments:*" or "*",
-- or an exclusion such as "!payments:legacy" applied after every include.
ALTER TABLE set_entries ADD COLUMN kind TEXT NOT NULL DEFAULT 'name'
    CHECK (kind IN ('name', 'pattern', 'exclusion'));

-- +goose Down
DELETE FROM set_entries WHERE kind <> 'name';
ALTER TABLE set_entries DROP COLUMN kind;
