package evalqueue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PoolExecutor is a Postgres-backed Executor. The table itself is the queue.
type PoolExecutor struct {
	db *pgxpool.Pool
}

// NewPool builds a PoolExecutor over db.
func NewPool(db *pgxpool.Pool) *PoolExecutor {
	return &PoolExecutor{db: db}
}

// jobColumns is the single definition of the column list scanJob expects, in
// order. Claim's UPDATE ... RETURNING (a later task) returns the same list.
const jobColumns = `id, skill_id, skill_name, version, suite_ref, tier, panel,
	status, attempts, max_attempts, worker_id, lease_expires_at, last_error,
	requested_by, created_at`

// row is the subset of pgx.Row/pgx.Rows that Scan needs.
type row interface {
	Scan(dest ...any) error
}

// scanJob scans a row shaped like jobColumns into a Job.
func scanJob(r row) (*Job, error) {
	var j Job
	var id, skillID string
	var panelJSON []byte
	err := r.Scan(
		&id, &skillID, &j.SkillName, &j.Version, &j.SuiteRef, &j.Tier, &panelJSON,
		&j.Status, &j.Attempts, &j.MaxAttempts, &j.WorkerID, &j.LeaseExpiresAt, &j.LastError,
		&j.RequestedBy, &j.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	j.ID = JobID(id)
	j.SkillID = skillID
	if len(panelJSON) > 0 {
		if err := json.Unmarshal(panelJSON, &j.Panel); err != nil {
			return nil, fmt.Errorf("evalqueue: unmarshal panel: %w", err)
		}
	}
	return &j, nil
}

// Submit enqueues a job. It does not deduplicate: two publishes of the same
// version are two measurements, and the caller decides whether that is wanted.
func (p *PoolExecutor) Submit(ctx context.Context, j Job) (JobID, error) {
	panelJSON, err := json.Marshal(j.Panel)
	if err != nil {
		return "", fmt.Errorf("evalqueue: marshal panel: %w", err)
	}
	tier := j.Tier
	if tier == "" {
		tier = "full"
	}
	requestedBy := j.RequestedBy
	if requestedBy == "" {
		requestedBy = "system"
	}
	var id string
	err = p.db.QueryRow(ctx, `
		INSERT INTO eval_jobs (skill_id, skill_name, version, suite_ref, tier, panel, requested_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		j.SkillID, j.SkillName, j.Version, j.SuiteRef, tier, panelJSON, requestedBy).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("evalqueue: submit: %w", err)
	}
	return JobID(id), nil
}

// Cancel stops a job that has not finished. A running job is cancelled too:
// the worker sees the status on its next heartbeat and abandons the run.
func (p *PoolExecutor) Cancel(ctx context.Context, id JobID) error {
	tag, err := p.db.Exec(ctx, `
		UPDATE eval_jobs SET status = 'cancelled', updated_at = now()
		WHERE id = $1 AND status IN ('queued', 'running')`, string(id))
	if err != nil {
		return fmt.Errorf("evalqueue: cancel: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotCancellable
	}
	return nil
}

// Get fetches a job by ID. A missing row is not an error: it returns
// (nil, nil), matching the repo convention (see auth.UserStore.GetByEmail).
func (p *PoolExecutor) Get(ctx context.Context, id JobID) (*Job, error) {
	row := p.db.QueryRow(ctx, `SELECT `+jobColumns+` FROM eval_jobs WHERE id = $1`, string(id))
	j, err := scanJob(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("evalqueue: get: %w", err)
	}
	return j, nil
}

// ListBySkill returns all jobs for a skill, most recent first.
func (p *PoolExecutor) ListBySkill(ctx context.Context, skillID string) ([]Job, error) {
	rows, err := p.db.Query(ctx, `SELECT `+jobColumns+` FROM eval_jobs WHERE skill_id = $1 ORDER BY created_at DESC`, skillID)
	if err != nil {
		return nil, fmt.Errorf("evalqueue: list by skill: %w", err)
	}
	defer rows.Close()

	var jobs []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, fmt.Errorf("evalqueue: list by skill: scan: %w", err)
		}
		jobs = append(jobs, *j)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("evalqueue: list by skill: %w", err)
	}
	return jobs, nil
}
