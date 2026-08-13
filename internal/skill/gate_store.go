package skill

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Executor is the subset of *pgxpool.Pool that a gate transition needs.
// Declared here rather than reused from internal/quality to avoid a cycle.
type Executor interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// Pool returns the underlying connection pool.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// ReleaseVersion moves a version to released. Uses the caller's Executor so
// ingestion can compose it with the quality upsert in one transaction.
// Re-releasing is a no-op, not an error.
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
		return fmt.Errorf("skill.Store.ReleaseVersion: %s v%d is absent or rejected", name, version)
	}

	// GREATEST, not a bare assignment: releasing a held v4 after v5 shipped
	// must not pull the pointer back.
	const advance = `
		UPDATE skills SET latest_version = GREATEST(latest_version, $2), updated_at = now()
		WHERE name = $1
	`
	if _, err := e.Exec(ctx, advance, name, version); err != nil {
		return fmt.Errorf("skill.Store.ReleaseVersion advance latest: %w", err)
	}

	// Backfill prose from whatever version the pointer ends up at (may differ
	// from $2 if GREATEST kept it higher). A held version wrote nothing to
	// the skills row on publish, so without this the skill serves stale prose.
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

	// Spec-derived columns (tags, author, etc.) are computed, not copied.
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

// RejectVersion marks a held version terminal.
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
