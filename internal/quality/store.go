package quality

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Executor is the subset of *pgxpool.Pool that Store's queries need.
type Executor interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// Store persists Records to skill_quality.
type Store struct {
	db Executor
}

// NewStore builds a Store over db.
func NewStore(db *pgxpool.Pool) *Store {
	return &Store{db: db}
}

// WithExecutor returns a Store whose queries run against e instead of the pool.
func (s *Store) WithExecutor(e Executor) *Store {
	return &Store{db: e}
}

// recordColumns is the column list scanRecord expects. suite_derived is
// computed live from the suite record (fail-closed: a missing row reads as
// derived). The stored column is the audit trail of what the gate saw.
const recordColumns = `skill_id, version, headline_score, headline_ci_low, headline_ci_high,
	pillar_breakdown, panel_matrix, robustness_gap, drift_grade, drift_breakdown,
	verified, panel_complete, suite_ref, engine_version, model_panel, tier, uplift_source, job_id, scored_at,
	critical_forbid_violations, judge_model,
	NOT EXISTS (SELECT 1 FROM eval_suites s
	            WHERE s.ref = skill_quality.suite_ref AND s.origin = 'authored')`

const getVersionColumns = recordColumns + `, report_json`

type row interface {
	Scan(dest ...any) error
}

// scanRecord scans a row shaped like recordColumns into a Record.
func scanRecord(r row) (*Record, error) { return scanRecordShape(r, false) }

// scanRecordWithReport scans a row shaped like getVersionColumns (recordColumns
// plus report_json) into a Record.
func scanRecordWithReport(r row) (*Record, error) { return scanRecordShape(r, true) }

func scanRecordShape(r row, withReport bool) (*Record, error) {
	var rec Record
	var jobID *string
	dest := []any{
		&rec.SkillID, &rec.Version, &rec.Headline, &rec.HeadlineCILow, &rec.HeadlineCIHigh,
		&rec.Pillars, &rec.PanelMatrix, &rec.RobustnessGap, &rec.DriftGrade, &rec.DriftBreakdown,
		&rec.Verified, &rec.PanelComplete, &rec.SuiteRef, &rec.EngineVersion, &rec.ModelPanel,
		&rec.Tier, &rec.UpliftSource, &jobID, &rec.ScoredAt,
		&rec.CriticalForbidViolations, &rec.JudgeModel, &rec.SuiteDerived,
	}
	if withReport {
		dest = append(dest, &rec.ReportJSON)
	}
	if err := r.Scan(dest...); err != nil {
		return nil, err
	}
	if jobID != nil {
		rec.JobID = *jobID
	}
	return &rec, nil
}

// Upsert inserts a new row. History is kept because version-over-version
// comparison depends on earlier rows surviving a re-score.
func (s *Store) Upsert(ctx context.Context, rec Record) error {
	var jobID *string
	if rec.JobID != "" {
		jobID = &rec.JobID
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO skill_quality (skill_id, version, headline_score, headline_ci_low, headline_ci_high,
			pillar_breakdown, panel_matrix, robustness_gap, drift_grade, drift_breakdown,
			verified, panel_complete, suite_ref, engine_version, model_panel, tier, uplift_source, job_id, scored_at,
			critical_forbid_violations, report_json, judge_model, suite_derived)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23)`,
		rec.SkillID, rec.Version, rec.Headline, rec.HeadlineCILow, rec.HeadlineCIHigh,
		rec.Pillars, rec.PanelMatrix, rec.RobustnessGap, rec.DriftGrade, rec.DriftBreakdown,
		rec.Verified, rec.PanelComplete, rec.SuiteRef, rec.EngineVersion, rec.ModelPanel,
		rec.Tier, rec.UpliftSource, jobID, rec.ScoredAt,
		rec.CriticalForbidViolations, rec.ReportJSON, rec.JudgeModel, rec.SuiteDerived)
	if err != nil {
		return fmt.Errorf("quality.Store.Upsert: %w", err)
	}
	return nil
}

// Latest returns the newest scored row for a skill version. Returns (nil, nil)
// when the version has never been scored.
func (s *Store) Latest(ctx context.Context, skillID string, version int) (*Record, error) {
	r := s.db.QueryRow(ctx, `SELECT `+recordColumns+`
		FROM skill_quality WHERE skill_id = $1 AND version = $2
		ORDER BY scored_at DESC, id DESC LIMIT 1`, skillID, version)
	rec, err := scanRecord(r)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("quality.Store.Latest: %w", err)
	}
	return rec, nil
}

// LatestAcrossVersions returns the newest scored row across all versions.
func (s *Store) LatestAcrossVersions(ctx context.Context, skillID string) (*Record, error) {
	r := s.db.QueryRow(ctx, `SELECT `+recordColumns+`
		FROM skill_quality WHERE skill_id = $1
		ORDER BY scored_at DESC, id DESC LIMIT 1`, skillID)
	rec, err := scanRecord(r)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("quality.Store.LatestAcrossVersions: %w", err)
	}
	return rec, nil
}

// GetVersion returns the score for one version, including the full report.
func (s *Store) GetVersion(ctx context.Context, skillID string, version int) (*Record, error) {
	row := s.db.QueryRow(ctx, `
		SELECT `+getVersionColumns+`
		FROM skill_quality
		WHERE skill_id = $1 AND version = $2
		ORDER BY scored_at DESC, id DESC
		LIMIT 1`, skillID, version)

	rec, err := scanRecordWithReport(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("quality.Store.GetVersion: %w", err)
	}
	return rec, nil
}

// History returns every scored row for a skill, across all versions, newest
// first.
func (s *Store) History(ctx context.Context, skillID string) ([]Record, error) {
	rows, err := s.db.Query(ctx, `SELECT `+recordColumns+`
		FROM skill_quality WHERE skill_id = $1 ORDER BY scored_at DESC, id DESC`, skillID)
	if err != nil {
		return nil, fmt.Errorf("quality.Store.History: %w", err)
	}
	defer rows.Close()

	var out []Record
	for rows.Next() {
		rec, err := scanRecord(rows)
		if err != nil {
			return nil, fmt.Errorf("quality.Store.History: scan: %w", err)
		}
		out = append(out, *rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("quality.Store.History: %w", err)
	}
	return out, nil
}
