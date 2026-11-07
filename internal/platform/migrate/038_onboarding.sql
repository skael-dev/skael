-- +goose Up
-- Sample data is marked, so one action removes all of it.
ALTER TABLE skills ADD COLUMN sample BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE skill_events ADD COLUMN sample BOOLEAN NOT NULL DEFAULT false;

-- The claim code a fresh instance's first owner enters, and when a CLI first
-- synced. One row each.
CREATE TABLE onboarding (
    id          INT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    claim_code  TEXT NOT NULL DEFAULT '',
    synced_at   TIMESTAMPTZ
);
INSERT INTO onboarding (id) VALUES (1);

-- +goose Down
DROP TABLE onboarding;
ALTER TABLE skill_events DROP COLUMN sample;
ALTER TABLE skills DROP COLUMN sample;
