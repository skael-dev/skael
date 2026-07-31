package evalqueue

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrLeaseLost is returned when a heartbeat or fail targets a job that is no
// longer this worker's — a cancelled job, or one another worker reclaimed.
var ErrLeaseLost = errors.New("evalqueue: lease lost")

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

// Claim takes one job under SKIP LOCKED so concurrent workers never contend
// on the same row and never block each other. Claimability covers two cases:
// a queued job, and a running job whose lease lapsed — the latter is how a
// worker that died mid-run gives its job back without anyone reaping it.
func (p *PoolExecutor) Claim(ctx context.Context, workerID string, lease time.Duration) (*Job, string, bool, error) {
	token, err := newToken()
	if err != nil {
		return nil, "", false, err
	}
	hash := sha256.Sum256([]byte(token))
	row := p.db.QueryRow(ctx, `
		WITH claimable AS (
			SELECT id AS claim_id FROM eval_jobs
			WHERE (status = 'queued'
			       OR (status = 'running' AND lease_expires_at < now()))
			  AND attempts < max_attempts
			ORDER BY created_at
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE eval_jobs j
		SET status = 'running',
		    attempts = j.attempts + 1,
		    worker_id = $1,
		    lease_expires_at = now() + make_interval(secs => $2),
		    claim_token_hash = $3,
		    updated_at = now()
		FROM claimable c
		WHERE j.id = c.claim_id
		RETURNING `+jobColumns, workerID, lease.Seconds(), hex.EncodeToString(hash[:]))

	j, err := scanJob(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", false, nil
	}
	if err != nil {
		return nil, "", false, fmt.Errorf("evalqueue: claim: %w", err)
	}
	return j, token, true, nil
}

// Heartbeat extends the lease only while this worker still owns the job and it
// is still running. A cancelled job therefore stops a worker at its next beat.
func (p *PoolExecutor) Heartbeat(ctx context.Context, id JobID, workerID string, lease time.Duration) error {
	tag, err := p.db.Exec(ctx, `
		UPDATE eval_jobs
		SET lease_expires_at = now() + make_interval(secs => $3), updated_at = now()
		WHERE id = $1 AND worker_id = $2 AND status = 'running'`,
		string(id), workerID, lease.Seconds())
	if err != nil {
		return fmt.Errorf("evalqueue: heartbeat: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrLeaseLost
	}
	return nil
}

// Complete marks a job done and clears its claim.
func (p *PoolExecutor) Complete(ctx context.Context, id JobID, workerID string) error {
	tag, err := p.db.Exec(ctx, `
		UPDATE eval_jobs
		SET status = 'done',
		    worker_id = '',
		    claim_token_hash = '',
		    lease_expires_at = NULL,
		    updated_at = now()
		WHERE id = $1 AND worker_id = $2 AND status = 'running'`,
		string(id), workerID)
	if err != nil {
		return fmt.Errorf("evalqueue: complete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrLeaseLost
	}
	return nil
}

// Fail returns the job to the pool while attempts remain and marks it failed
// once they do not. The cause is always recorded: a job that failed three
// times with no reason recorded is a support ticket.
func (p *PoolExecutor) Fail(ctx context.Context, id JobID, workerID, cause string) error {
	tag, err := p.db.Exec(ctx, `
		UPDATE eval_jobs
		SET status = CASE WHEN attempts >= max_attempts THEN 'failed' ELSE 'queued' END,
		    last_error = $3,
		    worker_id = '',
		    claim_token_hash = '',
		    lease_expires_at = NULL,
		    updated_at = now()
		WHERE id = $1 AND worker_id = $2 AND status = 'running'`,
		string(id), workerID, truncate(cause, 2000))
	if err != nil {
		return fmt.Errorf("evalqueue: fail: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrLeaseLost
	}
	return nil
}

// VerifyClaim compares in constant time: the claim token is a bearer
// credential and a byte-at-a-time comparison leaks it.
func (p *PoolExecutor) VerifyClaim(ctx context.Context, id JobID, token string) (*Job, bool, error) {
	j, err := p.Get(ctx, id)
	if err != nil || j == nil {
		return nil, false, err
	}
	var stored string
	if err := p.db.QueryRow(ctx,
		`SELECT claim_token_hash FROM eval_jobs WHERE id = $1`, string(id)).Scan(&stored); err != nil {
		return nil, false, err
	}
	want, err := hex.DecodeString(stored)
	if err != nil || len(want) == 0 {
		return nil, false, nil
	}
	got := sha256.Sum256([]byte(token))
	if subtle.ConstantTimeCompare(got[:], want) != 1 {
		return nil, false, nil
	}
	return j, true, nil
}

func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("evalqueue: mint token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// truncate caps s at n bytes so a runaway error message cannot blow past the
// column's practical size.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
