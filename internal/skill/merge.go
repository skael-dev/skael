package skill

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

func (s *Store) Merge(ctx context.Context, sourceName, targetName string) (*Skill, error) {
	if sourceName == targetName {
		return nil, fmt.Errorf("cannot merge a skill into itself")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("skill.Store.Merge begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var sourceID, targetID string
	err = tx.QueryRow(ctx, `SELECT id FROM skills WHERE name = $1`, sourceName).Scan(&sourceID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("source skill %q not found", sourceName)
		}
		return nil, fmt.Errorf("skill.Store.Merge source lookup: %w", err)
	}
	err = tx.QueryRow(ctx, `SELECT id FROM skills WHERE name = $1`, targetName).Scan(&targetID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("target skill %q not found", targetName)
		}
		return nil, fmt.Errorf("skill.Store.Merge target lookup: %w", err)
	}

	// Number the reparented versions from the target's highest existing
	// version, not from its latest_version. Those are no longer the same
	// number: a held version occupies a version number without the pointer
	// ever advancing to it, so numbering from the pointer would collide on
	// UNIQUE(skill_id, version).
	var targetMax int
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(MAX(version), 0) FROM skill_versions WHERE skill_id = $1`, targetID).Scan(&targetMax); err != nil {
		return nil, fmt.Errorf("skill.Store.Merge target max version: %w", err)
	}

	rows, err := tx.Query(ctx,
		`SELECT id, version, gate_state FROM skill_versions WHERE skill_id = $1 ORDER BY version ASC`, sourceID)
	if err != nil {
		return nil, fmt.Errorf("skill.Store.Merge list source versions: %w", err)
	}

	type versionRef struct {
		id        string
		version   int
		gateState string
	}
	var sourceVersions []versionRef
	for rows.Next() {
		var v versionRef
		if err := rows.Scan(&v.id, &v.version, &v.gateState); err != nil {
			rows.Close()
			return nil, fmt.Errorf("skill.Store.Merge scan version: %w", err)
		}
		sourceVersions = append(sourceVersions, v)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("skill.Store.Merge iterate source versions: %w", err)
	}

	// Reparent each source version to the target skill.
	// NOTE: archive_path values on skill_versions intentionally retain the
	// source skill's name prefix (e.g. "old-skill/abc123.tar.gz"). Storage
	// looks up archives by the stored path directly, so downloads continue to
	// work after a merge. Never clean storage directories by skill-name prefix
	// or these cross-name archives will be destroyed.
	//
	// gate_state is carried over untouched: a merge must not launder a held
	// version into a released one.
	highestReleased := 0
	for i, v := range sourceVersions {
		newVersion := targetMax + i + 1
		_, err := tx.Exec(ctx,
			`UPDATE skill_versions SET skill_id = $1, version = $2 WHERE id = $3`,
			targetID, newVersion, v.id)
		if err != nil {
			return nil, fmt.Errorf("skill.Store.Merge re-parent version %d: %w", v.version, err)
		}
		if v.gateState == "released" {
			highestReleased = newVersion
		}
	}

	// The pointer advances only as far as the highest reparented version that
	// is actually servable, and never backwards. Pointing it at a held version
	// would sync that bundle to every client — sync joins on
	// sv.version = s.latest_version — which is exactly what the gate exists to
	// prevent. If nothing reparented is released, the pointer does not move.
	if highestReleased > 0 {
		_, err = tx.Exec(ctx,
			`UPDATE skills SET latest_version = GREATEST(latest_version, $1), updated_at = now() WHERE id = $2`,
			highestReleased, targetID)
		if err != nil {
			return nil, fmt.Errorf("skill.Store.Merge update latest_version: %w", err)
		}
	} else if len(sourceVersions) > 0 {
		if _, err = tx.Exec(ctx, `UPDATE skills SET updated_at = now() WHERE id = $1`, targetID); err != nil {
			return nil, fmt.Errorf("skill.Store.Merge touch target: %w", err)
		}
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO skill_aliases (alias, canonical) VALUES ($1, $2) ON CONFLICT (alias) DO UPDATE SET canonical = $2`,
		sourceName, targetName)
	if err != nil {
		return nil, fmt.Errorf("skill.Store.Merge create alias: %w", err)
	}

	var targetHasSource bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM import_sources WHERE skill_id = $1)`, targetID).Scan(&targetHasSource); err != nil {
		return nil, fmt.Errorf("skill.Store.Merge check import_sources: %w", err)
	}
	if !targetHasSource {
		if _, err := tx.Exec(ctx, `UPDATE import_sources SET skill_id = $1 WHERE skill_id = $2`, targetID, sourceID); err != nil {
			return nil, fmt.Errorf("skill.Store.Merge transfer import_sources: %w", err)
		}
	}

	_, err = tx.Exec(ctx, `DELETE FROM skills WHERE id = $1`, sourceID)
	if err != nil {
		return nil, fmt.Errorf("skill.Store.Merge delete source: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("skill.Store.Merge commit: %w", err)
	}

	return s.GetByName(ctx, targetName)
}
