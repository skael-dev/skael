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
	var targetLatest int
	err = tx.QueryRow(ctx, `SELECT id FROM skills WHERE name = $1`, sourceName).Scan(&sourceID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("source skill %q not found", sourceName)
		}
		return nil, fmt.Errorf("skill.Store.Merge source lookup: %w", err)
	}
	err = tx.QueryRow(ctx, `SELECT id, latest_version FROM skills WHERE name = $1`, targetName).Scan(&targetID, &targetLatest)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("target skill %q not found", targetName)
		}
		return nil, fmt.Errorf("skill.Store.Merge target lookup: %w", err)
	}

	rows, err := tx.Query(ctx,
		`SELECT id, version FROM skill_versions WHERE skill_id = $1 ORDER BY version ASC`, sourceID)
	if err != nil {
		return nil, fmt.Errorf("skill.Store.Merge list source versions: %w", err)
	}

	type versionRef struct {
		id      string
		version int
	}
	var sourceVersions []versionRef
	for rows.Next() {
		var v versionRef
		if err := rows.Scan(&v.id, &v.version); err != nil {
			rows.Close()
			return nil, fmt.Errorf("skill.Store.Merge scan version: %w", err)
		}
		sourceVersions = append(sourceVersions, v)
	}
	rows.Close()

	for i, v := range sourceVersions {
		newVersion := targetLatest + i + 1
		_, err := tx.Exec(ctx,
			`UPDATE skill_versions SET skill_id = $1, version = $2 WHERE id = $3`,
			targetID, newVersion, v.id)
		if err != nil {
			return nil, fmt.Errorf("skill.Store.Merge re-parent version %d: %w", v.version, err)
		}
	}

	if len(sourceVersions) > 0 {
		newLatest := targetLatest + len(sourceVersions)
		_, err = tx.Exec(ctx,
			`UPDATE skills SET latest_version = $1, updated_at = now() WHERE id = $2`,
			newLatest, targetID)
		if err != nil {
			return nil, fmt.Errorf("skill.Store.Merge update latest_version: %w", err)
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
