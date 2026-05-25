package skillimport

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool *pgxpool.Pool
}

type ImportSource struct {
	ID          string     `json:"id"`
	SkillID     string     `json:"skill_id"`
	SkillName   string     `json:"skill_name,omitempty"`
	SourceType  string     `json:"source_type"`
	SourceURL   string     `json:"source_url"`
	SourcePath  string     `json:"source_path"`
	SourceRef   string     `json:"source_ref"`
	CommitSHA   string     `json:"commit_sha"`
	ImportedAt  time.Time  `json:"imported_at"`
	LastChecked *time.Time `json:"last_checked"`
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (s *Store) Upsert(ctx context.Context, src ImportSource) error {
	const q = `
		INSERT INTO import_sources (skill_id, source_type, source_url, source_path, source_ref, commit_sha)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (skill_id) DO UPDATE SET
			source_type = EXCLUDED.source_type,
			source_url  = EXCLUDED.source_url,
			source_path = EXCLUDED.source_path,
			source_ref  = EXCLUDED.source_ref,
			commit_sha  = EXCLUDED.commit_sha,
			imported_at = now()
	`
	_, err := s.pool.Exec(ctx, q,
		src.SkillID, src.SourceType, src.SourceURL, src.SourcePath, src.SourceRef, src.CommitSHA,
	)
	if err != nil {
		return fmt.Errorf("import.Store.Upsert: %w", err)
	}
	return nil
}

func (s *Store) GetBySkillID(ctx context.Context, skillID string) (*ImportSource, error) {
	const q = `
		SELECT id, skill_id, source_type, source_url, source_path, source_ref, commit_sha, imported_at, last_checked
		FROM import_sources
		WHERE skill_id = $1
	`
	var src ImportSource
	err := s.pool.QueryRow(ctx, q, skillID).Scan(
		&src.ID, &src.SkillID, &src.SourceType, &src.SourceURL,
		&src.SourcePath, &src.SourceRef, &src.CommitSHA, &src.ImportedAt, &src.LastChecked,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("import.Store.GetBySkillID: %w", err)
	}
	return &src, nil
}

func (s *Store) ListAll(ctx context.Context) ([]ImportSource, error) {
	const q = `
		SELECT i.id, i.skill_id, s.name, i.source_type, i.source_url, i.source_path, i.source_ref, i.commit_sha, i.imported_at, i.last_checked
		FROM import_sources i
		JOIN skills s ON s.id = i.skill_id
		ORDER BY i.imported_at DESC
	`
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("import.Store.ListAll query: %w", err)
	}
	defer rows.Close()

	var results []ImportSource
	for rows.Next() {
		var src ImportSource
		if err := rows.Scan(
			&src.ID, &src.SkillID, &src.SkillName, &src.SourceType, &src.SourceURL,
			&src.SourcePath, &src.SourceRef, &src.CommitSHA, &src.ImportedAt, &src.LastChecked,
		); err != nil {
			return nil, fmt.Errorf("import.Store.ListAll scan: %w", err)
		}
		results = append(results, src)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("import.Store.ListAll rows: %w", err)
	}
	if results == nil {
		results = []ImportSource{}
	}
	return results, nil
}
