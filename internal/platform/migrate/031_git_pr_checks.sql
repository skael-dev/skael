-- +goose Up
-- One Skael check run per pull request head on a connection.
CREATE TABLE git_pr_checks (
    connection_id       UUID NOT NULL REFERENCES git_connections(id) ON DELETE CASCADE,
    pr_number           INT NOT NULL,
    head_sha            TEXT NOT NULL,
    check_run_id        BIGINT NOT NULL DEFAULT 0,
    conclusion          TEXT NOT NULL,
    summary             TEXT NOT NULL DEFAULT '',
    pr_url              TEXT NOT NULL DEFAULT '',
    title               TEXT NOT NULL DEFAULT '',
    fork                BOOLEAN NOT NULL DEFAULT FALSE,
    -- A label, the check-run button or the Skael button asked for a contest.
    contest_requested   BOOLEAN NOT NULL DEFAULT FALSE,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (connection_id, pr_number, head_sha)
);

-- A PR head: one skill folder at one pull request head, stored so a contest
-- can evaluate it without a version.
CREATE TABLE git_pr_heads (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    connection_id       UUID NOT NULL REFERENCES git_connections(id) ON DELETE CASCADE,
    skill_id            UUID REFERENCES skills(id) ON DELETE CASCADE,
    skill_name          TEXT NOT NULL,
    pr_number           INT NOT NULL,
    head_sha            TEXT NOT NULL,
    digest              TEXT NOT NULL,
    archive_path        TEXT NOT NULL,
    decision            JSONB NOT NULL DEFAULT '{}',
    contest_id          UUID REFERENCES contests(id) ON DELETE SET NULL,
    contest_reported    BOOLEAN NOT NULL DEFAULT FALSE,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (connection_id, pr_number, head_sha, skill_name)
);
CREATE INDEX idx_git_pr_heads_digest ON git_pr_heads(skill_id, digest) WHERE contest_id IS NOT NULL;

-- A candidate is a version or a PR head. A PR head a contest references is
-- kept after its pull request closes.
ALTER TABLE contest_candidates ALTER COLUMN version DROP NOT NULL;
ALTER TABLE contest_candidates ADD COLUMN pr_head_id UUID REFERENCES git_pr_heads(id) ON DELETE RESTRICT;
ALTER TABLE contest_candidates ADD CONSTRAINT contest_candidates_version_or_pr_head
    CHECK (version IS NOT NULL OR pr_head_id IS NOT NULL);

-- The contest that evaluated a version's exact content as a PR head.
ALTER TABLE skill_versions ADD COLUMN contest_id UUID REFERENCES contests(id) ON DELETE SET NULL;

-- +goose Down
ALTER TABLE skill_versions DROP COLUMN contest_id;
ALTER TABLE contest_candidates DROP CONSTRAINT contest_candidates_version_or_pr_head;
ALTER TABLE contest_candidates DROP COLUMN pr_head_id;
DELETE FROM contest_candidates WHERE version IS NULL;
ALTER TABLE contest_candidates ALTER COLUMN version SET NOT NULL;
DROP TABLE git_pr_heads;
DROP TABLE git_pr_checks;
