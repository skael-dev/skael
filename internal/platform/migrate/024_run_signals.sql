-- +goose Up
-- consumer is the name, kept beside the id, so a run's record outlives its
-- service account. Neither table has an expiry: EVENT_RETENTION_DAYS purges
-- skill_events only, and a version comparison must give the same answer later.
CREATE TABLE runs (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    consumer        TEXT NOT NULL,
    consumer_id     UUID REFERENCES users(id) ON DELETE SET NULL,
    external_run_id TEXT NOT NULL,
    set_name        TEXT,
    resolution_id   TEXT,
    outcome         TEXT CHECK (outcome IN ('succeeded', 'failed', 'cancelled')),
    repairs         INT NOT NULL DEFAULT 0 CHECK (repairs >= 0),
    review_verdict  TEXT NOT NULL DEFAULT '',
    evidence_url    TEXT NOT NULL DEFAULT '',
    outcome_at      TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (consumer, external_run_id)
);

CREATE TABLE run_skills (
    run_id     UUID NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    skill_name TEXT NOT NULL,
    version    INT NOT NULL CHECK (version > 0),
    PRIMARY KEY (run_id, skill_name, version)
);
CREATE INDEX idx_run_skills_skill ON run_skills (skill_name, version);

ALTER TABLE skill_events
    ADD COLUMN version  INT,
    ADD COLUMN role     TEXT,
    ADD COLUMN consumer TEXT,
    ADD COLUMN run_id   UUID REFERENCES runs(id) ON DELETE SET NULL;

ALTER TABLE skill_events DROP CONSTRAINT skill_events_event_source_check;
ALTER TABLE skill_events
    ADD CONSTRAINT skill_events_event_source_check
    CHECK (event_source IN ('tool_invocation', 'transcript_scan', 'control_plane_report'));

CREATE INDEX idx_events_consumer ON skill_events (consumer, created_at DESC) WHERE consumer IS NOT NULL;

CREATE TABLE event_batches (
    consumer        TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    result          JSONB NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (consumer, idempotency_key)
);

-- A submission takes no version number and has no skill_versions row, which is
-- what keeps it out of every resolve, sync and download.
CREATE TABLE submissions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    skill_name      TEXT NOT NULL,
    consumer        TEXT NOT NULL,
    submitted_by    TEXT NOT NULL,
    rationale       TEXT NOT NULL,
    run_link        TEXT NOT NULL DEFAULT '',
    external_run_id TEXT NOT NULL DEFAULT '',
    archive_path    TEXT NOT NULL,
    checksum        TEXT NOT NULL,
    file_manifest   JSONB NOT NULL,
    scan_result     JSONB NOT NULL,
    state           TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'approved', 'rejected')),
    decided_by      TEXT NOT NULL DEFAULT '',
    decided_at      TIMESTAMPTZ,
    reason          TEXT NOT NULL DEFAULT '',
    edited          BOOLEAN NOT NULL DEFAULT FALSE,
    version         INT,
    -- The served version at decision time, so a decided diff keeps its baseline.
    diff_against    INT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_submissions_state ON submissions (state, created_at DESC);

ALTER TABLE skill_versions
    ADD COLUMN submission_id UUID REFERENCES submissions(id) ON DELETE SET NULL;

-- +goose Down
ALTER TABLE skill_versions DROP COLUMN IF EXISTS submission_id;
DROP TABLE IF EXISTS submissions;
DROP TABLE IF EXISTS event_batches;
DROP INDEX IF EXISTS idx_events_consumer;
DELETE FROM skill_events WHERE event_source = 'control_plane_report';
ALTER TABLE skill_events DROP CONSTRAINT skill_events_event_source_check;
ALTER TABLE skill_events
    ADD CONSTRAINT skill_events_event_source_check
    CHECK (event_source IN ('tool_invocation', 'transcript_scan'));
ALTER TABLE skill_events
    DROP COLUMN IF EXISTS run_id, DROP COLUMN IF EXISTS consumer,
    DROP COLUMN IF EXISTS role, DROP COLUMN IF EXISTS version;
DROP TABLE IF EXISTS run_skills;
DROP TABLE IF EXISTS runs;
