package skill

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Executor is the subset of *pgxpool.Pool that a gate transition needs. It is
// satisfied by both *pgxpool.Pool and pgx.Tx, so a caller can run a transition
// inside a transaction it shares with another store's write.
//
// It is declared here rather than reused from internal/quality because
// internal/quality imports internal/skill for its route wiring; importing it
// back would be a cycle. The shape is deliberately identical.
type Executor interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// Pool returns the underlying connection pool, so a caller with no transaction
// of its own can satisfy the Executor argument of ReleaseVersion.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// ReleaseVersion moves a version to released and advances the skill's latest
// pointer to it. It runs against e rather than the pool directly so report
// ingestion can compose it with the quality upsert and the job completion in
// one transaction: a release that lands while the score that justified it
// rolls back is a version published on evidence that does not exist.
//
// Releasing an already-released version is a no-op, not an error. Ingestion
// may re-decide a version a human already approved, and that race must not
// fail the worker's report.
func (s *Store) ReleaseVersion(ctx context.Context, e Executor, name string, version int, by, note string) error {
	const q = `
		UPDATE skill_versions sv
		SET gate_state = 'released', gated_by = $3, gated_at = now(), gate_note = $4
		FROM skills s
		WHERE sv.skill_id = s.id AND s.name = $1 AND sv.version = $2
		  AND sv.gate_state <> 'rejected'
	`
	tag, err := e.Exec(ctx, q, name, version, by, note)
	if err != nil {
		return fmt.Errorf("skill.Store.ReleaseVersion: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Either the version does not exist or it is rejected. Both are
		// caller errors and both must be loud: silently succeeding here
		// would report a release that did not happen.
		return fmt.Errorf("skill.Store.ReleaseVersion: %s v%d is absent or rejected", name, version)
	}

	// GREATEST, not a bare assignment. Releasing a held v4 after a clean v5
	// has already shipped must leave the pointer on v5; assigning would pull
	// every client back to the older skill.
	const advance = `
		UPDATE skills SET latest_version = GREATEST(latest_version, $2), updated_at = now()
		WHERE name = $1
	`
	if _, err := e.Exec(ctx, advance, name, version); err != nil {
		return fmt.Errorf("skill.Store.ReleaseVersion advance latest: %w", err)
	}

	// Advancing the pointer is not the whole release. A held version wrote
	// nothing to the skills row on publish — that is what stopped the gate
	// from shipping its prose while withholding its archive — so without this
	// the skill would serve the previous release's description and content
	// beside the new version's checksum, and its tags/author/license/
	// spec_compliance would stay empty forever (UpdateSpecFields is their only
	// writer, so the skill is invisible to tag filtering).
	//
	// The backfill is keyed on skills.latest_version, not on $2. The pointer
	// moved by GREATEST and may not have moved at all: releasing a held v4
	// after a clean v5 shipped leaves it on v5. The prose served must match
	// the version the pointer actually ends up at, so v4's release must not
	// pull v5's text back to v4's. Reading the pointer back is the only
	// formulation where those two can't disagree.
	const backfill = `
		UPDATE skills s
		SET description = sv.description,
		    content     = sv.content,
		    frontmatter = sv.frontmatter,
		    updated_at  = now()
		FROM skill_versions sv
		WHERE sv.skill_id = s.id AND sv.version = s.latest_version AND s.name = $1
	`
	if _, err := e.Exec(ctx, backfill, name); err != nil {
		return fmt.Errorf("skill.Store.ReleaseVersion backfill prose: %w", err)
	}

	// The spec-derived columns are computed from frontmatter rather than
	// stored, so they cannot be copied in SQL. Same rule as above: derive them
	// from whatever version the pointer now names.
	const pointedFrontmatter = `
		SELECT sv.frontmatter
		FROM skill_versions sv
		JOIN skills s ON s.id = sv.skill_id AND s.latest_version = sv.version
		WHERE s.name = $1
	`
	var rawFM []byte
	if err := e.QueryRow(ctx, pointedFrontmatter, name).Scan(&rawFM); err != nil {
		return fmt.Errorf("skill.Store.ReleaseVersion read pointed frontmatter: %w", err)
	}
	var fm map[string]interface{}
	if len(rawFM) > 0 {
		if err := json.Unmarshal(rawFM, &fm); err != nil {
			return fmt.Errorf("skill.Store.ReleaseVersion unmarshal frontmatter: %w", err)
		}
	}
	spec := ValidateSpec(fm, name)
	if err := updateSpecFieldsExec(ctx, e, name,
		spec.Author, spec.License, spec.Compat, spec.Compliance, spec.DisplayName, spec.Tags); err != nil {
		return fmt.Errorf("skill.Store.ReleaseVersion: %w", err)
	}
	return nil
}

// RejectVersion marks a held version terminal. The row stays: a deleted
// rejection teaches nobody anything, and the reason is the useful part.
func (s *Store) RejectVersion(ctx context.Context, name string, version int, by, note string) error {
	const q = `
		UPDATE skill_versions sv
		SET gate_state = 'rejected', gated_by = $3, gated_at = now(), gate_note = $4
		FROM skills s
		WHERE sv.skill_id = s.id AND s.name = $1 AND sv.version = $2
		  AND sv.gate_state = 'needs_review'
	`
	tag, err := s.pool.Exec(ctx, q, name, version, by, note)
	if err != nil {
		return fmt.Errorf("skill.Store.RejectVersion: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("skill.Store.RejectVersion: %s v%d is not awaiting review", name, version)
	}
	return nil
}
