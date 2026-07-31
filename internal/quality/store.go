package quality

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store persists Records to skill_quality.
type Store struct {
	db *pgxpool.Pool
}

// NewStore builds a Store over db.
func NewStore(db *pgxpool.Pool) *Store {
	return &Store{db: db}
}

// recordColumns is the single definition of the column list scanRecord
// expects, in order.
const recordColumns = `skill_id, version, headline_score, headline_ci_low, headline_ci_high,
	pillar_breakdown, panel_matrix, robustness_gap, drift_grade, drift_breakdown,
	verified, panel_complete, suite_ref, engine_version, model_panel, tier, job_id, scored_at`

// row is the subset of pgx.Row/pgx.Rows that scanRecord needs.
type row interface {
	Scan(dest ...any) error
}

// scanRecord scans a row shaped like recordColumns into a Record.
func scanRecord(r row) (*Record, error) {
	var rec Record
	var jobID *string
	err := r.Scan(
		&rec.SkillID, &rec.Version, &rec.Headline, &rec.HeadlineCILow, &rec.HeadlineCIHigh,
		&rec.Pillars, &rec.PanelMatrix, &rec.RobustnessGap, &rec.DriftGrade, &rec.DriftBreakdown,
		&rec.Verified, &rec.PanelComplete, &rec.SuiteRef, &rec.EngineVersion, &rec.ModelPanel,
		&rec.Tier, &jobID, &rec.ScoredAt,
	)
	if err != nil {
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
			verified, panel_complete, suite_ref, engine_version, model_panel, tier, job_id, scored_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)`,
		rec.SkillID, rec.Version, rec.Headline, rec.HeadlineCILow, rec.HeadlineCIHigh,
		rec.Pillars, rec.PanelMatrix, rec.RobustnessGap, rec.DriftGrade, rec.DriftBreakdown,
		rec.Verified, rec.PanelComplete, rec.SuiteRef, rec.EngineVersion, rec.ModelPanel,
		rec.Tier, jobID, rec.ScoredAt)
	if err != nil {
		return fmt.Errorf("quality.Store.Upsert: %w", err)
	}
	return nil
}

// Latest returns the newest scored row for a skill version, ordered by
// scored_at. A missing row is not an error: it returns (nil, nil).
func (s *Store) Latest(ctx context.Context, skillID string, version int) (*Record, error) {
	r := s.db.QueryRow(ctx, `SELECT `+recordColumns+`
		FROM skill_quality WHERE skill_id = $1 AND version = $2
		ORDER BY scored_at DESC LIMIT 1`, skillID, version)
	rec, err := scanRecord(r)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("quality.Store.Latest: %w", err)
	}
	return rec, nil
}

// History returns every scored row for a skill, across all versions, newest
// first.
func (s *Store) History(ctx context.Context, skillID string) ([]Record, error) {
	rows, err := s.db.Query(ctx, `SELECT `+recordColumns+`
		FROM skill_quality WHERE skill_id = $1 ORDER BY scored_at DESC`, skillID)
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
