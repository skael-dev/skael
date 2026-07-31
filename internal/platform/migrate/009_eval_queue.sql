-- +goose Up
-- Suites are stored, not regenerated: a score is a measurement against a
-- specific set of tasks, so re-running against a new model panel requires the
-- original tasks byte-for-byte. ref is the suite's content hash.
CREATE TABLE eval_suites (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    ref             TEXT NOT NULL UNIQUE,
    skill_name      TEXT NOT NULL,
    archive_path    TEXT NOT NULL,
    task_count      INT NOT NULL,
    -- The oracle-gate results the author recorded for exactly this ref. An
    -- eval against unchecked tasks cannot tell a broken task from a broken
    -- skill, so the check travels with the suite.
    checks          JSONB NOT NULL DEFAULT '[]',
    spec_version    INT NOT NULL DEFAULT 0,
    uploaded_by     TEXT NOT NULL DEFAULT 'system',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_eval_suites_skill ON eval_suites(skill_name);

CREATE TABLE eval_jobs (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    skill_id        UUID NOT NULL REFERENCES skills(id) ON DELETE CASCADE,
    skill_name      TEXT NOT NULL,
    version         INT NOT NULL,
    suite_ref       TEXT NOT NULL,
    tier            TEXT NOT NULL DEFAULT 'full',
    -- Requested panel, as {"agents":[...],"models":[...]}. Empty means the
    -- worker's default panel.
    panel           JSONB NOT NULL DEFAULT '{}',
    status          TEXT NOT NULL DEFAULT 'queued'
                    CHECK (status IN ('queued','running','done','failed','cancelled')),
    attempts        INT NOT NULL DEFAULT 0,
    max_attempts    INT NOT NULL DEFAULT 3,
    -- Lease: a claim sets worker_id and lease_expires_at; a heartbeat extends
    -- it; a lapsed lease returns the job to the pool.
    worker_id       TEXT NOT NULL DEFAULT '',
    lease_expires_at TIMESTAMPTZ,
    -- Bearer token minted at claim time. The worker posts its report with it,
    -- so ingestion authenticates the claim rather than the caller's role.
    claim_token_hash TEXT NOT NULL DEFAULT '',
    last_error      TEXT NOT NULL DEFAULT '',
    requested_by    TEXT NOT NULL DEFAULT 'system',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- The claim query's access path: oldest queued-or-lapsed job first.
CREATE INDEX idx_eval_jobs_claimable ON eval_jobs(status, lease_expires_at, created_at);
CREATE INDEX idx_eval_jobs_skill ON eval_jobs(skill_id, version);

CREATE TABLE skill_quality (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    skill_id           UUID NOT NULL REFERENCES skills(id) ON DELETE CASCADE,
    version            INT NOT NULL,
    headline_score     DOUBLE PRECISION NOT NULL,
    headline_ci_low    DOUBLE PRECISION NOT NULL DEFAULT 0,
    headline_ci_high   DOUBLE PRECISION NOT NULL DEFAULT 0,
    pillar_breakdown   JSONB NOT NULL DEFAULT '{}',
    panel_matrix       JSONB NOT NULL DEFAULT '{}',
    -- NULL means "we could not tell", which is not the same fact as 0.0
    -- ("the floor model kept up").
    robustness_gap     DOUBLE PRECISION,
    drift_grade        TEXT NOT NULL DEFAULT '',
    drift_breakdown    JSONB NOT NULL DEFAULT '{}',
    verified           BOOLEAN NOT NULL DEFAULT false,
    panel_complete     BOOLEAN NOT NULL DEFAULT false,
    suite_ref          TEXT NOT NULL,
    engine_version     TEXT NOT NULL DEFAULT '',
    model_panel        JSONB NOT NULL DEFAULT '[]',
    tier               TEXT NOT NULL DEFAULT '',
    job_id             UUID REFERENCES eval_jobs(id) ON DELETE SET NULL,
    scored_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- One current row per (skill, version, suite, panel-shape) is enforced in the
-- store's upsert rather than by a unique constraint, so adding org_id later is
-- an ADD COLUMN and nothing has to be rebuilt.
CREATE INDEX idx_skill_quality_lookup ON skill_quality(skill_id, version, scored_at DESC);

-- +goose Down
DROP TABLE skill_quality;
DROP TABLE eval_jobs;
DROP TABLE eval_suites;
