package contest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/skael-dev/skael/internal/evalqueue"
)

// Store persists contests.
type Store struct{ db *pgxpool.Pool }

// NewStore builds a Store over db.
func NewStore(db *pgxpool.Pool) *Store { return &Store{db: db} }

const contestColumns = `id, suite_ref, suite_provenance, suite_reviewed_by, suite_derived_from,
	tier, attempts, COALESCE(job_id::text, ''), status, outcome, winner, detail, pairings,
	last_error, requested_by, created_at, finished_at`

// Create records a requested contest and its candidates in one transaction, so
// a contest never exists without the entries it compares.
func (s *Store) Create(ctx context.Context, c *Contest) error {
	if len(c.Candidates) < 2 {
		return fmt.Errorf("contest.Store.Create: a contest needs at least two candidates, got %d", len(c.Candidates))
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("contest.Store.Create: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	err = tx.QueryRow(ctx, `
		INSERT INTO contests (suite_ref, suite_provenance, suite_reviewed_by, suite_derived_from,
			tier, attempts, status, requested_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, created_at`,
		c.SuiteRef, c.SuiteProvenance, c.SuiteReviewedBy, c.SuiteDerivedFrom,
		c.Tier, c.Attempts, StatusPending, c.RequestedBy,
	).Scan(&c.ID, &c.CreatedAt)
	if err != nil {
		return fmt.Errorf("contest.Store.Create: insert: %w", err)
	}

	for i := range c.Candidates {
		cand := &c.Candidates[i]
		cand.Position = i
		if _, err := tx.Exec(ctx, `
			INSERT INTO contest_candidates (contest_id, position, skill_id, skill_name, version, label)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			c.ID, cand.Position, cand.SkillID, cand.SkillName, cand.Version, cand.Label,
		); err != nil {
			return fmt.Errorf("contest.Store.Create: candidate %s: %w", cand.Label, err)
		}
	}
	c.Status = StatusPending
	return tx.Commit(ctx)
}

// AttachJob links the queued job that will run the contest.
func (s *Store) AttachJob(ctx context.Context, id, jobID string) error {
	_, err := s.db.Exec(ctx,
		`UPDATE contests SET job_id = $2, status = $3 WHERE id = $1`, id, jobID, StatusRunning)
	if err != nil {
		return fmt.Errorf("contest.Store.AttachJob: %w", err)
	}
	return nil
}

// Finish records the verdict, every candidate's report, and what each losing
// candidate missed. One transaction: a verdict without its evidence is the
// state this whole design exists to avoid.
func (s *Store) Finish(ctx context.Context, id string, v Verdict, reports map[string]json.RawMessage, losses []TaskLoss) error {
	pairings, err := json.Marshal(v.Pairings)
	if err != nil {
		return fmt.Errorf("contest.Store.Finish: marshal pairings: %w", err)
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("contest.Store.Finish: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		UPDATE contests
		SET status = $2, outcome = $3, winner = $4, detail = $5, pairings = $6, finished_at = now()
		WHERE id = $1`,
		id, StatusDone, v.Outcome, v.Winner, v.Detail, pairings,
	); err != nil {
		return fmt.Errorf("contest.Store.Finish: update: %w", err)
	}

	for label, raw := range reports {
		if _, err := tx.Exec(ctx,
			`UPDATE contest_candidates SET report_json = $3 WHERE contest_id = $1 AND label = $2`,
			id, label, raw,
		); err != nil {
			return fmt.Errorf("contest.Store.Finish: report for %s: %w", label, err)
		}
	}

	if _, err := tx.Exec(ctx, `DELETE FROM contest_task_losses WHERE contest_id = $1`, id); err != nil {
		return fmt.Errorf("contest.Store.Finish: clearing losses: %w", err)
	}
	for _, l := range losses {
		missed, err := json.Marshal(l.Missed)
		if err != nil {
			return fmt.Errorf("contest.Store.Finish: marshal missed: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO contest_task_losses (contest_id, label, task_id, missed, evidence)
			VALUES ($1, $2, $3, $4, $5)`,
			id, l.Label, l.TaskID, missed, l.Evidence,
		); err != nil {
			return fmt.Errorf("contest.Store.Finish: loss for %s: %w", l.Label, err)
		}
	}
	return tx.Commit(ctx)
}

// Fail records a contest that could not produce a verdict.
func (s *Store) Fail(ctx context.Context, id, reason string) error {
	_, err := s.db.Exec(ctx,
		`UPDATE contests SET status = $2, last_error = $3, finished_at = now() WHERE id = $1`,
		id, StatusFailed, reason)
	if err != nil {
		return fmt.Errorf("contest.Store.Fail: %w", err)
	}
	return nil
}

// CandidatesForJob returns the candidates a contest job must run, in order,
// with the attempts per task the contest asked for.
func (s *Store) CandidatesForJob(ctx context.Context, jobID string) ([]evalqueue.Candidate, int, error) {
	rows, err := s.db.Query(ctx, `
		SELECT cc.skill_id, cc.skill_name, cc.version, cc.label, c.attempts
		FROM contest_candidates cc
		JOIN contests c ON c.id = cc.contest_id
		WHERE c.job_id = $1
		ORDER BY cc.position`, jobID)
	if err != nil {
		return nil, 0, fmt.Errorf("contest.Store.CandidatesForJob: %w", err)
	}
	defer rows.Close()

	var out []evalqueue.Candidate
	attempts := 0
	for rows.Next() {
		var c evalqueue.Candidate
		if err := rows.Scan(&c.SkillID, &c.SkillName, &c.Version, &c.Label, &attempts); err != nil {
			return nil, 0, fmt.Errorf("contest.Store.CandidatesForJob: scan: %w", err)
		}
		out = append(out, c)
	}
	return out, attempts, rows.Err()
}

// Get returns one contest with its candidates and losses, or (nil, nil).
func (s *Store) Get(ctx context.Context, id string) (*Contest, error) {
	c, err := scanContest(s.db.QueryRow(ctx, `SELECT `+contestColumns+` FROM contests WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("contest.Store.Get: %w", err)
	}
	if err := s.loadParts(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

// ByJob returns the contest a job runs, or (nil, nil) for an ordinary eval job.
func (s *Store) ByJob(ctx context.Context, jobID string) (*Contest, error) {
	c, err := scanContest(s.db.QueryRow(ctx, `SELECT `+contestColumns+` FROM contests WHERE job_id = $1`, jobID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("contest.Store.ByJob: %w", err)
	}
	if err := s.loadParts(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

// ListForSkill returns every contest a skill entered, newest first. The chain
// is the subset where both candidates are that skill, which IsChainLink
// reports from the candidates rather than from a stored flag.
func (s *Store) ListForSkill(ctx context.Context, skillID string) ([]Contest, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+contestColumns+` FROM contests
		WHERE id IN (SELECT contest_id FROM contest_candidates WHERE skill_id = $1)
		ORDER BY created_at DESC`, skillID)
	if err != nil {
		return nil, fmt.Errorf("contest.Store.ListForSkill: %w", err)
	}
	defer rows.Close()

	var out []Contest
	for rows.Next() {
		c, err := scanContest(rows)
		if err != nil {
			return nil, fmt.Errorf("contest.Store.ListForSkill: scan: %w", err)
		}
		out = append(out, *c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("contest.Store.ListForSkill: %w", err)
	}
	for i := range out {
		if err := s.loadParts(ctx, &out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Store) loadParts(ctx context.Context, c *Contest) error {
	rows, err := s.db.Query(ctx, `
		SELECT position, skill_id, skill_name, version, label, report_json
		FROM contest_candidates WHERE contest_id = $1 ORDER BY position`, c.ID)
	if err != nil {
		return fmt.Errorf("contest.Store: candidates: %w", err)
	}
	defer rows.Close()
	c.Candidates = nil
	for rows.Next() {
		var cand Candidate
		if err := rows.Scan(&cand.Position, &cand.SkillID, &cand.SkillName, &cand.Version, &cand.Label, &cand.Report); err != nil {
			return fmt.Errorf("contest.Store: scan candidate: %w", err)
		}
		c.Candidates = append(c.Candidates, cand)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("contest.Store: candidates: %w", err)
	}

	lrows, err := s.db.Query(ctx, `
		SELECT label, task_id, missed, evidence
		FROM contest_task_losses WHERE contest_id = $1 ORDER BY label, task_id`, c.ID)
	if err != nil {
		return fmt.Errorf("contest.Store: losses: %w", err)
	}
	defer lrows.Close()
	c.Losses = nil
	for lrows.Next() {
		var l TaskLoss
		var missed []byte
		if err := lrows.Scan(&l.Label, &l.TaskID, &missed, &l.Evidence); err != nil {
			return fmt.Errorf("contest.Store: scan loss: %w", err)
		}
		if err := json.Unmarshal(missed, &l.Missed); err != nil {
			return fmt.Errorf("contest.Store: decode missed: %w", err)
		}
		c.Losses = append(c.Losses, l)
	}
	return lrows.Err()
}

type scanner interface{ Scan(dest ...any) error }

func scanContest(r scanner) (*Contest, error) {
	var c Contest
	var pairings []byte
	if err := r.Scan(&c.ID, &c.SuiteRef, &c.SuiteProvenance, &c.SuiteReviewedBy, &c.SuiteDerivedFrom,
		&c.Tier, &c.Attempts, &c.JobID, &c.Status, &c.Outcome, &c.Winner, &c.Detail, &pairings,
		&c.LastError, &c.RequestedBy, &c.CreatedAt, &c.FinishedAt); err != nil {
		return nil, err
	}
	if len(pairings) > 0 {
		if err := json.Unmarshal(pairings, &c.Pairings); err != nil {
			return nil, fmt.Errorf("decode pairings: %w", err)
		}
	}
	return &c, nil
}
