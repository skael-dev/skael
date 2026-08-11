package quality

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Executor is the subset of *pgxpool.Pool that Store's queries need. It is
// satisfied by both *pgxpool.Pool and pgx.Tx, so a caller can run a Store
// write inside a transaction shared with another store (see WithExecutor).
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

// WithExecutor returns a Store whose queries run against e (typically a
// pgx.Tx) instead of the pool directly, so a caller can compose a write with
// another store's write in one transaction.
func (s *Store) WithExecutor(e Executor) *Store {
	return &Store{db: e}
}

// recordColumns is the single definition of the column list scanRecord
// expects, in order.
//
// suite_derived is computed from the suite record rather than read from the
// column of the same name. The column is stamped once at report time. A
// review that raises a suite to authored afterwards cannot clear the badge
// it exists to clear. The stored column stays as the audit trail of what
// the release gate saw at that moment. Nothing serves it.
//
// The check tests NOT EXISTS for an authored suite, not EXISTS for a
// derived one. The two forms agree for every row that exists, since origin
// only ever holds one of two values. A missing row then reads as derived.
// This keeps the flag fail closed rather than permissive.
const recordColumns = `skill_id, version, headline_score, headline_ci_low, headline_ci_high,
	pillar_breakdown, panel_matrix, robustness_gap, drift_grade, drift_breakdown,
	verified, panel_complete, suite_ref, engine_version, model_panel, tier, uplift_source, job_id, scored_at,
	critical_forbid_violations, judge_model,
	NOT EXISTS (SELECT 1 FROM eval_suites s
	            WHERE s.ref = skill_quality.suite_ref AND s.origin = 'authored')`

// getVersionColumns is recordColumns plus the report payload. It is the only
// column list that selects report_json: the summary and history reads stay
// narrow because a report is orders of magnitude larger than the aggregates
// beside it, and a list endpoint that returned one per row would be unusable.
const getVersionColumns = recordColumns + `, report_json`

// row is the subset of pgx.Row/pgx.Rows that scanRecord needs.
type row interface {
	Scan(dest ...any) error
}

// scanRecord scans a row shaped like recordColumns into a Record.
func scanRecord(r row) (*Record, error) { return scanRecordShape(r, false) }

// scanRecordWithReport scans a row shaped like getVersionColumns (recordColumns
// plus report_json) into a Record.
func scanRecordWithReport(r row) (*Record, error) { return scanRecordShape(r, true) }

// scanRecordShape scans a row shaped like recordColumns, optionally followed
// by report_json. withReport tells it which of the two shapes it was handed;
// there is no way to infer that from a pgx.Row.
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

// Upsert inserts a new row every time. Despite the name this is deliberately
// not an update-in-place: history is kept because version-over-version
// comparison depends on the earlier rows surviving a re-score.
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

// Latest returns the newest scored row for a skill version, ordered by
// scored_at with id as a deterministic tiebreak — two rows can share an
// identical scored_at (timestamp truncation, or two ingestions in one
// request), and without a secondary key Postgres gives no guarantee which
// one comes back. A missing row is not an error: it returns (nil, nil).
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

// LatestAcrossVersions returns the newest scored row for a skill, across all
// of its versions, ordered by scored_at with id as a deterministic tiebreak
// (see Latest). Unlike Latest, it is not pinned to a specific version — a
// skill's quality badge should keep showing its most recent score even while
// a newer, not-yet-scored version is the current one; otherwise every
// publish makes the badge vanish until the next eval lands. A missing row is
// not an error: it returns (nil, nil).
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

// GetVersion returns the score for one specific version, including the full
// stored report. Ordered by scored_at with id as a deterministic tiebreak
// (see Latest) so re-fetching one version cannot return different rows on
// different requests. Returns (nil, nil) when that version has never been
// scored.
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
