-- +goose Up
-- The users.role default was 'admin', so every account after the first one —
-- which signup explicitly creates as 'owner' — was created privileged. Make
-- the default unprivileged and constrain role to the three roles the product
-- actually knows about: owner, admin, member.
ALTER TABLE users ALTER COLUMN role SET DEFAULT 'member';

-- Rows created under the old default were 'admin' by accident, not by grant.
UPDATE users SET role = 'member' WHERE role = 'admin';

ALTER TABLE users
    ADD CONSTRAINT users_role_check
    CHECK (role IN ('owner', 'admin', 'member'));

-- +goose Down
-- The backfill above destroys the information about which rows were 'admin',
-- so Down restores the schema but cannot restore per-row roles. That is
-- deliberate: those rows were never deliberately granted admin.
ALTER TABLE users DROP CONSTRAINT users_role_check;
ALTER TABLE users ALTER COLUMN role SET DEFAULT 'admin';
