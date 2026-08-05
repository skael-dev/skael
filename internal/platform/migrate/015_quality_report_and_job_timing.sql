-- +goose Up
-- report_json holds the worker's full eval report, which ingestion previously
-- discarded after extracting aggregates. Nullable because every row written
-- before this migration has none: the detail page degrades to aggregates
-- rather than failing.
ALTER TABLE skill_quality ADD COLUMN report_json JSONB;

-- started_at records when a job first entered `running`. Elapsed time cannot
-- be derived from lease_expires_at, which every heartbeat moves forward.
ALTER TABLE eval_jobs ADD COLUMN started_at TIMESTAMPTZ;

-- +goose Down
ALTER TABLE skill_quality DROP COLUMN report_json;
ALTER TABLE eval_jobs DROP COLUMN started_at;
