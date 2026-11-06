-- +goose Up
-- A connection is one repo, branch, root path and name prefix whose skill
-- folders Skael syncs. It publishes as its own service account.
CREATE TABLE git_connections (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    installation_id     BIGINT NOT NULL,
    repo_owner          TEXT NOT NULL,
    repo_name           TEXT NOT NULL,
    branch              TEXT NOT NULL,
    root_path           TEXT NOT NULL DEFAULT '',
    prefix              TEXT NOT NULL,
    contest_mode        TEXT NOT NULL DEFAULT 'on_request'
                        CHECK (contest_mode IN ('off', 'on_request', 'ready')),
    service_account_id  UUID NOT NULL REFERENCES auth_users(id) ON DELETE RESTRICT,
    created_by          TEXT NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    status              TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'ok', 'error')),
    last_error          TEXT NOT NULL DEFAULT '',
    last_synced_sha     TEXT NOT NULL DEFAULT '',
    last_sync_at        TIMESTAMPTZ,
    last_webhook_at     TIMESTAMPTZ,
    UNIQUE (repo_owner, repo_name, branch, root_path)
);

-- A git-bound skill. One skill has one source, so the skill is the key.
CREATE TABLE git_bindings (
    skill_id        UUID PRIMARY KEY REFERENCES skills(id) ON DELETE CASCADE,
    connection_id   UUID NOT NULL REFERENCES git_connections(id) ON DELETE CASCADE,
    path            TEXT NOT NULL,
    last_commit     TEXT NOT NULL DEFAULT '',
    bound_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_git_bindings_connection ON git_bindings(connection_id);

-- A detached skill's name, which the connection never adopts again.
CREATE TABLE git_connection_ignores (
    connection_id   UUID NOT NULL REFERENCES git_connections(id) ON DELETE CASCADE,
    skill_name      TEXT NOT NULL,
    PRIMARY KEY (connection_id, skill_name)
);

-- The folders the last sync refused, with the reason. Replaced on each sync.
CREATE TABLE git_connection_refusals (
    connection_id   UUID NOT NULL REFERENCES git_connections(id) ON DELETE CASCADE,
    path            TEXT NOT NULL,
    skill_name      TEXT NOT NULL,
    reason          TEXT NOT NULL,
    PRIMARY KEY (connection_id, path)
);

-- A git-bound skill whose folder left the repo keeps serving its last version.
ALTER TABLE skills ADD COLUMN removed_from_git_at TIMESTAMPTZ;
ALTER TABLE skills ADD COLUMN removed_from_git_commit TEXT NOT NULL DEFAULT '';

-- An exact pin follows a rename, because version numbers do not change, and
-- never follows a merge. Every existing alias came from a merge.
ALTER TABLE skill_aliases ADD COLUMN reason TEXT NOT NULL DEFAULT 'merge'
    CHECK (reason IN ('merge', 'rename'));

-- +goose Down
ALTER TABLE skill_aliases DROP COLUMN reason;
ALTER TABLE skills DROP COLUMN removed_from_git_commit;
ALTER TABLE skills DROP COLUMN removed_from_git_at;
DROP TABLE git_connection_refusals;
DROP TABLE git_connection_ignores;
DROP TABLE git_bindings;
DROP TABLE git_connections;
