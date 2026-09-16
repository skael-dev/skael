-- +goose Up
-- A contest compares candidates by running them together. It advises and never
-- gates: nothing here clears a hold or moves skills.latest_version.
CREATE TABLE contests (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    suite_ref       TEXT NOT NULL,
    -- Who chose the tasks. 'authored' carries a reviewer; 'derived_all' is
    -- generated from every candidate; 'derived_one' is generated from the
    -- candidate named in suite_derived_from, which grades the others against
    -- that candidate's claims and is therefore disclosed on every read.
    suite_provenance TEXT NOT NULL
                    CHECK (suite_provenance IN ('authored','derived_all','derived_one')),
    suite_reviewed_by  TEXT NOT NULL DEFAULT '',
    suite_derived_from TEXT NOT NULL DEFAULT '',
    tier            TEXT NOT NULL DEFAULT 'full',
    -- Attempts per task per candidate. One number for the whole contest: a
    -- candidate given more attempts than another is not being compared to it.
    attempts        INT NOT NULL DEFAULT 3 CHECK (attempts > 0),
    job_id          UUID REFERENCES eval_jobs(id) ON DELETE SET NULL,
    status          TEXT NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending','running','done','failed')),
    outcome         TEXT NOT NULL DEFAULT '',
    winner          TEXT NOT NULL DEFAULT '',
    detail          TEXT NOT NULL DEFAULT '',
    pairings        JSONB NOT NULL DEFAULT '[]',
    last_error      TEXT NOT NULL DEFAULT '',
    requested_by    TEXT NOT NULL DEFAULT 'system',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at     TIMESTAMPTZ
);

-- A candidate is a published version, so it is already content-addressed,
-- scanned and attributable. label is what the verdict names.
CREATE TABLE contest_candidates (
    contest_id      UUID NOT NULL REFERENCES contests(id) ON DELETE CASCADE,
    position        INT NOT NULL,
    skill_id        UUID NOT NULL REFERENCES skills(id) ON DELETE CASCADE,
    skill_name      TEXT NOT NULL,
    version         INT NOT NULL,
    label           TEXT NOT NULL,
    report_json     JSONB,
    PRIMARY KEY (contest_id, position),
    UNIQUE (contest_id, label)
);
CREATE INDEX idx_contest_candidates_skill ON contest_candidates(skill_id, version);

-- What a losing candidate missed, per task. This is the input a person reads
-- to fix the skill by hand, and the input a tuner would consume later.
CREATE TABLE contest_task_losses (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    contest_id      UUID NOT NULL REFERENCES contests(id) ON DELETE CASCADE,
    label           TEXT NOT NULL,
    task_id         TEXT NOT NULL,
    missed          JSONB NOT NULL DEFAULT '[]',
    evidence        TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_contest_task_losses_contest ON contest_task_losses(contest_id, label);

-- A contest job carries several candidates, so the worker must know it is one.
-- eval_jobs.skill_id and version keep the first candidate, which leaves every
-- existing query on that table correct.
ALTER TABLE eval_jobs ADD COLUMN contest_id UUID REFERENCES contests(id) ON DELETE CASCADE;

-- +goose Down
ALTER TABLE eval_jobs DROP COLUMN contest_id;
DROP TABLE contest_task_losses;
DROP TABLE contest_candidates;
DROP TABLE contests;
