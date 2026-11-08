package platform

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresStorage keeps archives in the archives table, so one database backup
// holds everything.
type PostgresStorage struct {
	pool *pgxpool.Pool
}

// NewPostgresStorage returns a store on pool.
func NewPostgresStorage(pool *pgxpool.Pool) *PostgresStorage { return &PostgresStorage{pool: pool} }

// Write stores r under name, replacing an archive with the same name.
func (s *PostgresStorage) Write(ctx context.Context, name string, r io.Reader) (string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", fmt.Errorf("storage: read %s: %w", name, err)
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO archives (name, data) VALUES ($1, $2)
		ON CONFLICT (name) DO UPDATE SET data = EXCLUDED.data, created_at = now()`, name, data)
	if err != nil {
		return "", fmt.Errorf("storage: write %s: %w", name, err)
	}
	return name, nil
}

// Read returns the archive under name. A missing archive is fs.ErrNotExist.
func (s *PostgresStorage) Read(ctx context.Context, name string) (io.ReadCloser, error) {
	var data []byte
	err := s.pool.QueryRow(ctx, `SELECT data FROM archives WHERE name = $1`, name).Scan(&data)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("storage: %s: %w", name, fs.ErrNotExist)
	}
	if err != nil {
		return nil, fmt.Errorf("storage: read %s: %w", name, err)
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

// Delete removes the archive under name.
func (s *PostgresStorage) Delete(ctx context.Context, name string) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM archives WHERE name = $1`, name); err != nil {
		return fmt.Errorf("storage: delete %s: %w", name, err)
	}
	return nil
}

// Ping checks that the archives table answers.
func (s *PostgresStorage) Ping(ctx context.Context) error {
	var one int
	if err := s.pool.QueryRow(ctx, `SELECT 1 FROM archives LIMIT 1`).Scan(&one); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("storage: ping: %w", err)
	}
	return nil
}

// Archive store kinds recorded in storage_backend.
const (
	BackendFilesystem = "filesystem"
	BackendPostgres   = "postgres"
)

// OpenStorage returns the archive store. A set storagePath wins. Unset, the
// install keeps the store it started with, and a new install uses Postgres.
// defaultPath is the filesystem store of an install that started on it.
func OpenStorage(ctx context.Context, pool *pgxpool.Pool, storagePath, defaultPath string) (Storage, string, error) {
	if storagePath != "" {
		st, err := NewStorageFromConfig(&Config{StoragePath: storagePath})
		return st, storagePath, err
	}
	var kind string
	err := pool.QueryRow(ctx, `SELECT kind FROM storage_backend WHERE id = 1`).Scan(&kind)
	if errors.Is(err, pgx.ErrNoRows) {
		kind = BackendPostgres
		if _, err := pool.Exec(ctx, `INSERT INTO storage_backend (kind) VALUES ($1) ON CONFLICT DO NOTHING`, kind); err != nil {
			return nil, "", fmt.Errorf("storage: record backend: %w", err)
		}
		if err := pool.QueryRow(ctx, `SELECT kind FROM storage_backend WHERE id = 1`).Scan(&kind); err != nil {
			return nil, "", fmt.Errorf("storage: read backend: %w", err)
		}
	} else if err != nil {
		return nil, "", fmt.Errorf("storage: read backend: %w", err)
	}
	if kind == BackendFilesystem {
		st, err := NewLocalStorage(defaultPath)
		return st, defaultPath, err
	}
	return NewPostgresStorage(pool), "postgres", nil
}
