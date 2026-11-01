-- +goose Up
ALTER TABLE users
    ADD COLUMN kind TEXT NOT NULL DEFAULT 'human',
    ADD CONSTRAINT users_kind_check CHECK (kind IN ('human', 'service'));

-- set_id has no foreign key on purpose. Deleting a set must leave the key
-- pointing at nothing, so it returns 403, and ON DELETE SET NULL would turn a
-- set-limited key into an unrestricted one.
ALTER TABLE user_api_keys
    ADD COLUMN scopes TEXT[] NOT NULL DEFAULT '{}',
    ADD COLUMN set_id UUID;

CREATE TABLE sets (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    created_by  TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Entries name a skill rather than reference it, because a deleted skill must
-- stay visible as an excluded entry and not vanish from the set. Version 0
-- means latest.
CREATE TABLE set_entries (
    set_id     UUID NOT NULL REFERENCES sets(id) ON DELETE CASCADE,
    skill_name TEXT NOT NULL,
    version    INT NOT NULL DEFAULT 0 CHECK (version >= 0),
    PRIMARY KEY (set_id, skill_name)
);

-- No foreign key to sets and no expiry: a run's record names a resolution id
-- that must still mean the same versions after the set is gone.
CREATE TABLE set_resolutions (
    id         TEXT NOT NULL,
    set_id     UUID NOT NULL,
    set_name   TEXT NOT NULL,
    entries    JSONB NOT NULL,
    complete   BOOLEAN NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (set_id, id)
);

CREATE TABLE set_callers (
    set_id        UUID NOT NULL REFERENCES sets(id) ON DELETE CASCADE,
    user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    caller_name   TEXT NOT NULL,
    resolution_id TEXT NOT NULL,
    last_seen_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (set_id, user_id)
);

-- actor is a name, not a foreign key: the record outlives the account.
CREATE TABLE audit_log (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    actor      TEXT NOT NULL,
    action     TEXT NOT NULL,
    resource   TEXT NOT NULL,
    detail     JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_audit_log_created_at ON audit_log (created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS audit_log;
DROP TABLE IF EXISTS set_callers;
DROP TABLE IF EXISTS set_resolutions;
DROP TABLE IF EXISTS set_entries;
DROP TABLE IF EXISTS sets;
ALTER TABLE user_api_keys DROP COLUMN IF EXISTS set_id, DROP COLUMN IF EXISTS scopes;
DELETE FROM users WHERE kind = 'service';
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_kind_check, DROP COLUMN IF EXISTS kind;
