// Package runs records what each automated run loaded and how it ended.
package runs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/skael-dev/skael/internal/audit"
)

// MinComparableRuns is the sample below which a panel states that the sample is
// too small. Below it a rate reads as a finding when it is noise.
const MinComparableRuns = 20

// Consumer is the identity a request reports as, taken from its account.
type Consumer struct {
	ID   string
	Name string
}

// Event is one activation inside a run.
type Event struct {
	Skill         string
	Version       int
	Agent         string
	Role          string
	ExternalRunID string
	Time          time.Time
	Set           string
	ResolutionID  string
}

// BatchResult is the stored answer to one idempotency key.
type BatchResult struct {
	Stored int `json:"stored"`
	Runs   int `json:"runs"`
}

// Store persists runs.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore constructs a Store.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// upsertRun creates the run on the first event or outcome that names it, since
// arrival order is not guaranteed, and fills a set the run did not yet record.
func upsertRun(ctx context.Context, tx pgx.Tx, c Consumer, externalID, set, resolutionID string) (string, error) {
	var id string
	err := tx.QueryRow(ctx, `
		INSERT INTO runs (consumer, consumer_id, external_run_id, set_name, resolution_id)
		VALUES ($1, $2, $3, NULLIF($4, ''), NULLIF($5, ''))
		ON CONFLICT (consumer, external_run_id) DO UPDATE SET
			set_name = COALESCE(runs.set_name, EXCLUDED.set_name),
			resolution_id = COALESCE(runs.resolution_id, EXCLUDED.resolution_id)
		RETURNING id`, c.Name, c.ID, externalID, set, resolutionID).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("runs: upsert run: %w", err)
	}
	return id, nil
}

// InsertBatch stores a batch all or nothing. A repeated key returns the first
// result and stores nothing.
func (s *Store) InsertBatch(ctx context.Context, c Consumer, key string, events []Event) (*BatchResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("runs.InsertBatch: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if key != "" {
		// The insert takes the lock, so a concurrent retry waits here until
		// this batch commits, and then reads the stored result.
		tag, err := tx.Exec(ctx, `
			INSERT INTO event_batches (consumer, idempotency_key, result) VALUES ($1, $2, '{}')
			ON CONFLICT (consumer, idempotency_key) DO NOTHING`, c.Name, key)
		if err != nil {
			return nil, fmt.Errorf("runs.InsertBatch: claim key: %w", err)
		}
		if tag.RowsAffected() == 0 {
			var raw []byte
			if err := tx.QueryRow(ctx, `SELECT result FROM event_batches WHERE consumer = $1 AND idempotency_key = $2`, c.Name, key).Scan(&raw); err != nil {
				return nil, fmt.Errorf("runs.InsertBatch: read key: %w", err)
			}
			var first BatchResult
			if err := json.Unmarshal(raw, &first); err != nil {
				return nil, fmt.Errorf("runs.InsertBatch: read key: %w", err)
			}
			return &first, nil
		}
	}

	runIDs := map[string]string{}
	for _, e := range events {
		runID, seen := runIDs[e.ExternalRunID]
		if !seen {
			if runID, err = upsertRun(ctx, tx, c, e.ExternalRunID, e.Set, e.ResolutionID); err != nil {
				return nil, err
			}
			runIDs[e.ExternalRunID] = runID
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO skill_events (skill_name, agent, trigger_type, event_source, registered, created_at, version, role, consumer, run_id)
			VALUES ($1, $2, 'auto', 'control_plane_report', EXISTS (
				SELECT 1 FROM skills s
				WHERE s.name = COALESCE((SELECT a.canonical FROM skill_aliases a WHERE a.alias = $1), $1)
			), $3, $4, $5, $6, $7)`, e.Skill, e.Agent, e.Time, e.Version, e.Role, c.Name, runID)
		if err != nil {
			return nil, fmt.Errorf("runs.InsertBatch: event: %w", err)
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO run_skills (run_id, skill_name, version) VALUES ($1, $2, $3)
			ON CONFLICT DO NOTHING`, runID, e.Skill, e.Version)
		if err != nil {
			return nil, fmt.Errorf("runs.InsertBatch: run skill: %w", err)
		}
	}

	result := &BatchResult{Stored: len(events), Runs: len(runIDs)}
	if key != "" {
		raw, err := json.Marshal(result)
		if err != nil {
			return nil, fmt.Errorf("runs.InsertBatch: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE event_batches SET result = $3 WHERE consumer = $1 AND idempotency_key = $2`, c.Name, key, raw); err != nil {
			return nil, fmt.Errorf("runs.InsertBatch: store result: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("runs.InsertBatch: %w", err)
	}
	return result, nil
}

// Outcome is how a run ended.
type Outcome struct {
	ExternalRunID string
	Outcome       string
	Repairs       int
	ReviewVerdict string
	EvidenceURL   string
	Set           string
	ResolutionID  string
}

// RunRecord is the stored run.
type RunRecord struct {
	ID            string     `json:"id"`
	Consumer      string     `json:"consumer"`
	ExternalRunID string     `json:"external_run_id"`
	Set           *string    `json:"set,omitempty"`
	ResolutionID  *string    `json:"resolution_id,omitempty"`
	Outcome       *string    `json:"outcome,omitempty"`
	Repairs       int        `json:"repairs"`
	ReviewVerdict string     `json:"review_verdict"`
	EvidenceURL   string     `json:"evidence_url"`
	OutcomeAt     *time.Time `json:"outcome_at,omitempty"`
}

// PutOutcome records an outcome. A second outcome replaces the first, because a
// late review verdict is a legitimate correction, and the audit log keeps the
// old value.
func (s *Store) PutOutcome(ctx context.Context, c Consumer, o Outcome) (*RunRecord, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("runs.PutOutcome: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	id, err := upsertRun(ctx, tx, c, o.ExternalRunID, o.Set, o.ResolutionID)
	if err != nil {
		return nil, err
	}
	const columns = `id, consumer, external_run_id, set_name, resolution_id, outcome, repairs, review_verdict, evidence_url, outcome_at`
	var old RunRecord
	err = tx.QueryRow(ctx, `SELECT `+columns+` FROM runs WHERE id = $1 FOR UPDATE`, id).Scan(
		&old.ID, &old.Consumer, &old.ExternalRunID, &old.Set, &old.ResolutionID, &old.Outcome, &old.Repairs, &old.ReviewVerdict, &old.EvidenceURL, &old.OutcomeAt)
	if err != nil {
		return nil, fmt.Errorf("runs.PutOutcome: %w", err)
	}
	var rec RunRecord
	err = tx.QueryRow(ctx, `
		UPDATE runs SET outcome = $2, repairs = $3, review_verdict = $4, evidence_url = $5, outcome_at = now()
		WHERE id = $1 RETURNING `+columns, id, o.Outcome, o.Repairs, o.ReviewVerdict, o.EvidenceURL).Scan(
		&rec.ID, &rec.Consumer, &rec.ExternalRunID, &rec.Set, &rec.ResolutionID, &rec.Outcome, &rec.Repairs, &rec.ReviewVerdict, &rec.EvidenceURL, &rec.OutcomeAt)
	if err != nil {
		return nil, fmt.Errorf("runs.PutOutcome: %w", err)
	}
	if old.Outcome != nil {
		if err := audit.Record(ctx, tx, c.Name, "run.outcome.replace", "run:"+c.Name+"/"+o.ExternalRunID, map[string]any{"old": old}); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("runs.PutOutcome: %w", err)
	}
	return &rec, nil
}

// VersionOutcomes summarises one consumer's runs that loaded one version.
type VersionOutcomes struct {
	Version   int `json:"version"`
	Runs      int `json:"runs" doc:"Runs that succeeded or failed. This is the sample size."`
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
	Cancelled int `json:"cancelled"`
	NoOutcome int `json:"no_outcome"`
	// SuccessRate and MeanRepairs are nil below MinComparableRuns.
	SuccessRate    *float64           `json:"success_rate"`
	MeanRepairs    *float64           `json:"mean_repairs"`
	SampleTooSmall bool               `json:"sample_too_small"`
	Comparison     *OutcomeComparison `json:"comparison,omitempty"`

	repairsTotal int
}

// OutcomeComparison sets a version beside the previous released version. It
// states a difference and no cause.
type OutcomeComparison struct {
	PreviousVersion   int     `json:"previous_version"`
	SuccessRateChange float64 `json:"success_rate_change"`
	MeanRepairsChange float64 `json:"mean_repairs_change"`
}

// ConsumerOutcomes is one consumer's panel.
type ConsumerOutcomes struct {
	Consumer string            `json:"consumer"`
	Versions []VersionOutcomes `json:"versions"`
}

// SkillOutcomes reads run_skills only: a skill that was available and not
// loaded did not influence the run, and skill_events may have been purged.
func (s *Store) SkillOutcomes(ctx context.Context, skill string) ([]ConsumerOutcomes, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT r.consumer, rs.version,
		       COUNT(*) FILTER (WHERE r.outcome = 'succeeded')::int,
		       COUNT(*) FILTER (WHERE r.outcome = 'failed')::int,
		       COUNT(*) FILTER (WHERE r.outcome = 'cancelled')::int,
		       COUNT(*) FILTER (WHERE r.outcome IS NULL)::int,
		       COALESCE(SUM(r.repairs) FILTER (WHERE r.outcome IN ('succeeded', 'failed')), 0)::int
		FROM run_skills rs
		JOIN runs r ON r.id = rs.run_id
		LEFT JOIN skill_aliases a ON a.alias = rs.skill_name
		WHERE COALESCE(a.canonical, rs.skill_name) = $1
		GROUP BY r.consumer, rs.version
		ORDER BY r.consumer, rs.version DESC`, skill)
	if err != nil {
		return nil, fmt.Errorf("runs.SkillOutcomes: %w", err)
	}
	defer rows.Close()

	out := []ConsumerOutcomes{}
	for rows.Next() {
		var consumer string
		var v VersionOutcomes
		if err := rows.Scan(&consumer, &v.Version, &v.Succeeded, &v.Failed, &v.Cancelled, &v.NoOutcome, &v.repairsTotal); err != nil {
			return nil, fmt.Errorf("runs.SkillOutcomes: %w", err)
		}
		v.Runs = v.Succeeded + v.Failed
		if len(out) == 0 || out[len(out)-1].Consumer != consumer {
			out = append(out, ConsumerOutcomes{Consumer: consumer})
		}
		out[len(out)-1].Versions = append(out[len(out)-1].Versions, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("runs.SkillOutcomes: %w", err)
	}

	released, err := s.releasedVersions(ctx, skill)
	if err != nil {
		return nil, err
	}
	for i := range out {
		applyComparisons(out[i].Versions, released)
	}
	return out, nil
}

func (s *Store) releasedVersions(ctx context.Context, skill string) ([]int, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT v.version FROM skill_versions v JOIN skills sk ON sk.id = v.skill_id
		WHERE sk.name = $1 AND v.gate_state = 'released' ORDER BY v.version`, skill)
	if err != nil {
		return nil, fmt.Errorf("runs: released versions: %w", err)
	}
	versions, err := pgx.CollectRows(rows, pgx.RowTo[int])
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("runs: released versions: %w", err)
	}
	return versions, nil
}

// applyComparisons fills the rates of versions with enough runs, and compares a
// version with the previous released version only when both have enough.
func applyComparisons(versions []VersionOutcomes, released []int) {
	byVersion := map[int]*VersionOutcomes{}
	for i := range versions {
		v := &versions[i]
		byVersion[v.Version] = v
		v.SampleTooSmall = v.Runs < MinComparableRuns
		if !v.SampleTooSmall {
			rate := float64(v.Succeeded) / float64(v.Runs)
			repairs := float64(v.repairsTotal) / float64(v.Runs)
			v.SuccessRate, v.MeanRepairs = &rate, &repairs
		}
	}
	for i := range versions {
		v := &versions[i]
		at, found := slices.BinarySearch(released, v.Version)
		if !found || at == 0 || v.SampleTooSmall {
			continue
		}
		prev, ok := byVersion[released[at-1]]
		if !ok || prev.SampleTooSmall {
			continue
		}
		v.Comparison = &OutcomeComparison{
			PreviousVersion:   prev.Version,
			SuccessRateChange: *v.SuccessRate - *prev.SuccessRate,
			MeanRepairsChange: *v.MeanRepairs - *prev.MeanRepairs,
		}
	}
}
