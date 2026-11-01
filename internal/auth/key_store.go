package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type APIKeyRow struct {
	ID         string
	UserID     string
	Name       string
	KeyPrefix  string
	KeyHash    string
	LastUsedAt *time.Time
	CreatedAt  time.Time
	Scopes     []string
	SetID      *string
}

type APIKeyInfo struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	LastUsedAt *time.Time `json:"last_used_at"`
	CreatedAt  time.Time  `json:"created_at"`
	Scopes     []string   `json:"scopes"`
	SetID      *string    `json:"set_id,omitempty"`
}

type KeyStore struct {
	pool *pgxpool.Pool
}

func NewKeyStore(pool *pgxpool.Pool) *KeyStore {
	return &KeyStore{pool: pool}
}

func (s *KeyStore) Create(ctx context.Context, userID, name, keyPrefix, keyHash string) (*APIKeyRow, error) {
	return s.CreateLimited(ctx, userID, name, keyPrefix, keyHash, nil, nil)
}

// CreateLimited inserts a key with scopes and an optional set limit.
func (s *KeyStore) CreateLimited(ctx context.Context, userID, name, keyPrefix, keyHash string, scopes []string, setID *string) (*APIKeyRow, error) {
	if scopes == nil {
		scopes = []string{}
	}
	const q = `
        INSERT INTO user_api_keys (user_id, name, key_prefix, key_hash, scopes, set_id)
        VALUES ($1, $2, $3, $4, $5, $6)
        RETURNING id, user_id, name, key_prefix, key_hash, last_used_at, created_at, scopes, set_id
    `
	var k APIKeyRow
	err := s.pool.QueryRow(ctx, q, userID, name, keyPrefix, keyHash, scopes, setID).Scan(
		&k.ID, &k.UserID, &k.Name, &k.KeyPrefix, &k.KeyHash, &k.LastUsedAt, &k.CreatedAt, &k.Scopes, &k.SetID,
	)
	if err != nil {
		return nil, fmt.Errorf("auth.KeyStore.CreateLimited: %w", err)
	}
	return &k, nil
}

func (s *KeyStore) ListByUser(ctx context.Context, userID string) ([]APIKeyInfo, error) {
	const q = `
        SELECT id, name, key_prefix, last_used_at, created_at, scopes, set_id
        FROM user_api_keys WHERE user_id = $1 ORDER BY created_at DESC
    `
	rows, err := s.pool.Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("auth.KeyStore.ListByUser: %w", err)
	}
	defer rows.Close()
	var keys []APIKeyInfo
	for rows.Next() {
		var k APIKeyInfo
		if err := rows.Scan(&k.ID, &k.Name, &k.Prefix, &k.LastUsedAt, &k.CreatedAt, &k.Scopes, &k.SetID); err != nil {
			return nil, fmt.Errorf("auth.KeyStore.ListByUser scan: %w", err)
		}
		keys = append(keys, k)
	}
	if keys == nil {
		keys = []APIKeyInfo{}
	}
	return keys, rows.Err()
}

func (s *KeyStore) GetByPrefix(ctx context.Context, prefix string) (*APIKeyRow, error) {
	const q = `
        SELECT id, user_id, name, key_prefix, key_hash, last_used_at, created_at, scopes, set_id
        FROM user_api_keys WHERE key_prefix = $1
    `
	var k APIKeyRow
	err := s.pool.QueryRow(ctx, q, prefix).Scan(
		&k.ID, &k.UserID, &k.Name, &k.KeyPrefix, &k.KeyHash, &k.LastUsedAt, &k.CreatedAt, &k.Scopes, &k.SetID,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("auth.KeyStore.GetByPrefix: %w", err)
	}
	return &k, nil
}

func (s *KeyStore) Delete(ctx context.Context, id, userID string) error {
	const q = `DELETE FROM user_api_keys WHERE id = $1 AND user_id = $2`
	tag, err := s.pool.Exec(ctx, q, id, userID)
	if err != nil {
		return fmt.Errorf("auth.KeyStore.Delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("auth.KeyStore.Delete: key not found")
	}
	return nil
}

func (s *KeyStore) UpdateLastUsed(ctx context.Context, id string) {
	s.pool.Exec(ctx, `UPDATE user_api_keys SET last_used_at = now() WHERE id = $1`, id) //nolint:errcheck
}
