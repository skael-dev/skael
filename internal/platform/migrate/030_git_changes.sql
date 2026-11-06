-- +goose Up
-- An outbound change: the PR Skael opens for a publish to a git-bound skill.
-- It gets a version when it merges and the merged folder publishes.
CREATE TABLE git_changes (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    connection_id   UUID NOT NULL REFERENCES git_connections(id) ON DELETE CASCADE,
    skill_id        UUID NOT NULL REFERENCES skills(id) ON DELETE CASCADE,
    branch          TEXT NOT NULL,
    pr_number       INT NOT NULL,
    pr_url          TEXT NOT NULL,
    head_sha        TEXT NOT NULL,
    -- The checksum skill.Pack gives the committed folder, which an inbound
    -- sync of the merged folder reproduces.
    digest          TEXT NOT NULL,
    requested_by    TEXT NOT NULL,
    state           TEXT NOT NULL DEFAULT 'open' CHECK (state IN ('open', 'merged', 'closed')),
    merge_sha       TEXT NOT NULL DEFAULT '',
    version         INT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_git_changes_open ON git_changes(connection_id) WHERE state = 'open' OR version IS NULL;
CREATE INDEX idx_git_changes_skill ON git_changes(skill_id, created_at DESC);

-- A submission approved for a git-bound skill waits on its change. Its state
-- follows the change: merged approves it, closed rejects it.
ALTER TABLE submissions ADD COLUMN change_id UUID REFERENCES git_changes(id) ON DELETE SET NULL;

-- +goose Down
ALTER TABLE submissions DROP COLUMN change_id;
DROP TABLE git_changes;
