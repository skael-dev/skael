package skill

import (
	"context"
	"encoding/json"
	"errors"
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

// ReleaseVersion moves a version to released. A rejected or withdrawn version
// never moves: only a new version replaces it. Uses the caller's Executor so
// ingestion can compose it with the quality upsert in one transaction.
// Re-releasing is a no-op, not an error.
func (s *Store) ReleaseVersion(ctx context.Context, e Executor, name string, version int, by, note string) error {
	// A second release of a released version is a no-op, so it must emit nothing.
	var priorState, checksum string
	err := e.QueryRow(ctx, `
		SELECT sv.gate_state, sv.checksum FROM skill_versions sv JOIN skills s ON s.id = sv.skill_id
		WHERE s.name = $1 AND sv.version = $2 FOR UPDATE OF sv`, name, version).Scan(&priorState, &checksum)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("skill.Store.ReleaseVersion: %w", err)
	}
	const q = `
		UPDATE skill_versions sv
		SET gate_state = 'released', gated_by = $3, gated_at = now(), gate_note = $4
		FROM skills s
		WHERE sv.skill_id = s.id AND s.name = $1 AND sv.version = $2
		  AND sv.gate_state NOT IN ('rejected', 'withdrawn')
	`
	tag, err := e.Exec(ctx, q, name, version, by, note)
	if err != nil {
		return fmt.Errorf("skill.Store.ReleaseVersion: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("skill.Store.ReleaseVersion: %s v%d is absent, rejected or withdrawn", name, version)
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

	if err := servePointedProse(ctx, e, name); err != nil {
		return fmt.Errorf("skill.Store.ReleaseVersion: %w", err)
	}
	if s.notifier == nil || priorState == "released" {
		return nil
	}
	// Every production caller passes a transaction, which ties the events to
	// the release. A caller with a bare pool still gets its events.
	ev := ReleaseEvent{Skill: name, Version: version, Checksum: checksum, Approved: true}
	if tx, ok := e.(pgx.Tx); ok {
		return s.notifier.VersionReleased(ctx, tx, ev)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("skill.Store.ReleaseVersion: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := s.notifier.VersionReleased(ctx, tx, ev); err != nil {
		return err
	}
	return tx.Commit(ctx)
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

// servePointedProse copies the prose and the spec-derived columns of the
// version latest_version points at onto the skills row. A held version wrote
// nothing to that row on publish, and a withdrawn one must stop being served,
// so both a release and a withdrawal end here. With no released version left,
// the row serves no body at all.
func servePointedProse(ctx context.Context, e Executor, name string) error {
	const backfill = `
		UPDATE skills s
		SET description = sv.description,
		    content     = sv.content,
		    frontmatter = sv.frontmatter,
		    updated_at  = now()
		FROM skill_versions sv
		WHERE sv.skill_id = s.id AND sv.version = s.latest_version AND s.name = $1
	`
	tag, err := e.Exec(ctx, backfill, name)
	if err != nil {
		return fmt.Errorf("backfill prose: %w", err)
	}
	var rawFM []byte
	if tag.RowsAffected() == 0 {
		const clear = `
			UPDATE skills SET content = '', frontmatter = '{}', updated_at = now()
			WHERE name = $1 AND latest_version = 0
		`
		if _, err := e.Exec(ctx, clear, name); err != nil {
			return fmt.Errorf("clear prose: %w", err)
		}
	} else {
		const pointedFrontmatter = `
			SELECT sv.frontmatter
			FROM skill_versions sv
			JOIN skills s ON s.id = sv.skill_id AND s.latest_version = sv.version
			WHERE s.name = $1
		`
		if err := e.QueryRow(ctx, pointedFrontmatter, name).Scan(&rawFM); err != nil {
			return fmt.Errorf("read pointed frontmatter: %w", err)
		}
	}
	// Spec-derived columns (tags, author, etc.) are computed, not copied.
	var fm map[string]interface{}
	if len(rawFM) > 0 {
		if err := json.Unmarshal(rawFM, &fm); err != nil {
			return fmt.Errorf("unmarshal frontmatter: %w", err)
		}
	}
	spec := ValidateSpec(fm, name)
	return updateSpecFieldsExec(ctx, e, name,
		spec.Author, spec.License, spec.Compat, spec.Compliance, spec.DisplayName, spec.Tags)
}
