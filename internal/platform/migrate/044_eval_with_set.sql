-- +goose Up
-- An eval can install a set beside the skill under test. The job pins one
-- stored resolution, so a re-run installs the same versions after a member
-- publishes a new one. set_members is that resolution without the skill under
-- test, with each member's authored suite at the time the job was created.
ALTER TABLE eval_jobs ADD COLUMN set_id UUID;
ALTER TABLE eval_jobs ADD COLUMN set_name TEXT NOT NULL DEFAULT '';
ALTER TABLE eval_jobs ADD COLUMN set_resolution_id TEXT;
ALTER TABLE eval_jobs ADD COLUMN set_members JSONB NOT NULL DEFAULT '[]';
ALTER TABLE eval_jobs ADD CONSTRAINT eval_jobs_set_resolution
    FOREIGN KEY (set_id, set_resolution_id) REFERENCES set_resolutions(set_id, id);

-- A contest candidate is a version, a PR head or a set resolution. A set
-- candidate has no single skill.
ALTER TABLE contest_candidates ALTER COLUMN skill_id DROP NOT NULL;
ALTER TABLE contest_candidates ADD COLUMN set_id UUID;
ALTER TABLE contest_candidates ADD COLUMN set_name TEXT NOT NULL DEFAULT '';
ALTER TABLE contest_candidates ADD COLUMN resolution_id TEXT;
ALTER TABLE contest_candidates ADD COLUMN set_members JSONB NOT NULL DEFAULT '[]';
ALTER TABLE contest_candidates ADD CONSTRAINT contest_candidates_set_resolution
    FOREIGN KEY (set_id, resolution_id) REFERENCES set_resolutions(set_id, id);
ALTER TABLE contest_candidates DROP CONSTRAINT contest_candidates_version_or_pr_head;
ALTER TABLE contest_candidates ADD CONSTRAINT contest_candidates_one_kind CHECK (
    (version IS NOT NULL)::int + (pr_head_id IS NOT NULL)::int + (resolution_id IS NOT NULL)::int = 1);
ALTER TABLE contest_candidates ADD CONSTRAINT contest_candidates_skill_or_set
    CHECK (skill_id IS NOT NULL OR resolution_id IS NOT NULL);

-- The skills that fired on a task a set candidate lost.
ALTER TABLE contest_task_losses ADD COLUMN fired JSONB NOT NULL DEFAULT '[]';

-- +goose Down
ALTER TABLE contest_task_losses DROP COLUMN fired;
DELETE FROM contests WHERE id IN (SELECT contest_id FROM contest_candidates WHERE resolution_id IS NOT NULL);
ALTER TABLE contest_candidates DROP CONSTRAINT contest_candidates_skill_or_set;
ALTER TABLE contest_candidates DROP CONSTRAINT contest_candidates_one_kind;
ALTER TABLE contest_candidates ADD CONSTRAINT contest_candidates_version_or_pr_head
    CHECK (version IS NOT NULL OR pr_head_id IS NOT NULL);
ALTER TABLE contest_candidates DROP CONSTRAINT contest_candidates_set_resolution;
ALTER TABLE contest_candidates DROP COLUMN set_members;
ALTER TABLE contest_candidates DROP COLUMN resolution_id;
ALTER TABLE contest_candidates DROP COLUMN set_name;
ALTER TABLE contest_candidates DROP COLUMN set_id;
ALTER TABLE contest_candidates ALTER COLUMN skill_id SET NOT NULL;
DELETE FROM eval_jobs WHERE set_resolution_id IS NOT NULL;
ALTER TABLE eval_jobs DROP CONSTRAINT eval_jobs_set_resolution;
ALTER TABLE eval_jobs DROP COLUMN set_members;
ALTER TABLE eval_jobs DROP COLUMN set_resolution_id;
ALTER TABLE eval_jobs DROP COLUMN set_name;
ALTER TABLE eval_jobs DROP COLUMN set_id;
