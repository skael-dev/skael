-- +goose Up
-- Record, at ingest time, whether the reported skill name was in the registry.
-- Activation queries stay dynamic — they re-check against skills on read, so a
-- name registered later starts counting — but this column preserves what was
-- known when the event arrived, which a read-time check cannot reconstruct.
ALTER TABLE skill_events ADD COLUMN registered BOOLEAN NOT NULL DEFAULT TRUE;

UPDATE skill_events se
SET registered = EXISTS (
    SELECT 1 FROM skills s
    WHERE s.name = COALESCE(
        (SELECT a.canonical FROM skill_aliases a WHERE a.alias = se.skill_name),
        se.skill_name
    )
);

CREATE INDEX idx_events_unregistered ON skill_events (created_at DESC) WHERE registered = FALSE;

-- +goose Down
DROP INDEX IF EXISTS idx_events_unregistered;
ALTER TABLE skill_events DROP COLUMN registered;
