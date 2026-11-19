package sync

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ManifestEntry represents a single skill's sync metadata for client diffing.
type ManifestEntry struct {
	Name     string `json:"name"`
	Version  int    `json:"version"`
	Checksum string `json:"checksum"`
}

// Store handles sync-related queries against Postgres.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore constructs a Store backed by the given connection pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// GetManifest returns the manifest of all published skills, ordered by name.
// Only skills with latest_version > 0 are included.
func (s *Store) GetManifest(ctx context.Context) ([]ManifestEntry, error) {
	const q = `
		SELECT s.name, s.latest_version, sv.checksum
		FROM skills s
		JOIN skill_versions sv ON sv.skill_id = s.id AND sv.version = s.latest_version
		WHERE s.latest_version > 0
		ORDER BY s.name
	`
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("sync.Store.GetManifest query: %w", err)
	}
	defer rows.Close()

	var entries []ManifestEntry
	for rows.Next() {
		var e ManifestEntry
		if err := rows.Scan(&e.Name, &e.Version, &e.Checksum); err != nil {
			return nil, fmt.Errorf("sync.Store.GetManifest scan: %w", err)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sync.Store.GetManifest rows: %w", err)
	}
	return entries, nil
}

// WithdrawnEntry is one withdrawn version. A client that has it installed
// removes it and prints Reason.
type WithdrawnEntry struct {
	Name     string `json:"name"`
	Version  int    `json:"version"`
	Checksum string `json:"checksum"`
	Reason   string `json:"reason"`
}

// Withdrawn returns every withdrawn version, ordered by name and version.
func (s *Store) Withdrawn(ctx context.Context) ([]WithdrawnEntry, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT s.name, sv.version, sv.checksum, sv.gate_note
		FROM skill_versions sv JOIN skills s ON s.id = sv.skill_id
		WHERE sv.gate_state = 'withdrawn'
		ORDER BY s.name, sv.version`)
	if err != nil {
		return nil, fmt.Errorf("sync.Store.Withdrawn: %w", err)
	}
	entries, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (WithdrawnEntry, error) {
		var e WithdrawnEntry
		return e, r.Scan(&e.Name, &e.Version, &e.Checksum, &e.Reason)
	})
	if err != nil {
		return nil, fmt.Errorf("sync.Store.Withdrawn: %w", err)
	}
	if entries == nil {
		entries = []WithdrawnEntry{}
	}
	return entries, nil
}
