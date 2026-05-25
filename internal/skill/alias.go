package skill

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type Alias struct {
	Alias     string    `json:"alias"`
	Canonical string    `json:"canonical"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Store) CreateAlias(ctx context.Context, alias, canonical string) error {
	const q = `INSERT INTO skill_aliases (alias, canonical) VALUES ($1, $2) ON CONFLICT (alias) DO UPDATE SET canonical = $2`
	if _, err := s.pool.Exec(ctx, q, alias, canonical); err != nil {
		return fmt.Errorf("skill.Store.CreateAlias: %w", err)
	}
	return nil
}

func (s *Store) DeleteAlias(ctx context.Context, alias string) error {
	const q = `DELETE FROM skill_aliases WHERE alias = $1`
	if _, err := s.pool.Exec(ctx, q, alias); err != nil {
		return fmt.Errorf("skill.Store.DeleteAlias: %w", err)
	}
	return nil
}

func (s *Store) ListAliases(ctx context.Context, canonical string) ([]Alias, error) {
	const q = `SELECT alias, canonical, created_at FROM skill_aliases WHERE canonical = $1 ORDER BY alias`
	rows, err := s.pool.Query(ctx, q, canonical)
	if err != nil {
		return nil, fmt.Errorf("skill.Store.ListAliases query: %w", err)
	}
	defer rows.Close()

	var results []Alias
	for rows.Next() {
		var a Alias
		if err := rows.Scan(&a.Alias, &a.Canonical, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("skill.Store.ListAliases scan: %w", err)
		}
		results = append(results, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("skill.Store.ListAliases rows: %w", err)
	}
	if results == nil {
		results = []Alias{}
	}
	return results, nil
}

func (s *Store) ResolveAlias(ctx context.Context, name string) (string, error) {
	const q = `SELECT canonical FROM skill_aliases WHERE alias = $1`
	var canonical string
	err := s.pool.QueryRow(ctx, q, name).Scan(&canonical)
	if err == pgx.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("skill.Store.ResolveAlias: %w", err)
	}
	return canonical, nil
}
