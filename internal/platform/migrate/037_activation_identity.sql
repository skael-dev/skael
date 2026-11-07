-- +goose Up
-- Who ran the skill, on which model and harness version. user_id comes from the
-- authenticated key and is NULL when the org turns identity off.
ALTER TABLE skill_events
    ADD COLUMN user_id         UUID REFERENCES auth_users(id) ON DELETE SET NULL,
    ADD COLUMN model           TEXT,
    ADD COLUMN harness_version TEXT;

CREATE INDEX idx_events_user ON skill_events (user_id, created_at DESC) WHERE user_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_events_user;
ALTER TABLE skill_events DROP COLUMN harness_version, DROP COLUMN model, DROP COLUMN user_id;
