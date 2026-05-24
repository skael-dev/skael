package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type UserRow struct {
	ID           string
	Email        string
	Name         string
	PasswordHash string
	Role         string
	CreatedAt    time.Time
}

type UserStore struct {
	pool *pgxpool.Pool
}

func NewUserStore(pool *pgxpool.Pool) *UserStore {
	return &UserStore{pool: pool}
}

func (s *UserStore) Create(ctx context.Context, email, name, passwordHash string) (*UserRow, error) {
	const q = `
        INSERT INTO users (email, name, password_hash)
        VALUES ($1, $2, $3)
        RETURNING id, email, name, password_hash, role, created_at
    `
	var u UserRow
	err := s.pool.QueryRow(ctx, q, email, name, passwordHash).Scan(
		&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.Role, &u.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("auth.UserStore.Create: %w", err)
	}
	return &u, nil
}

func (s *UserStore) CreateWithRole(ctx context.Context, email, name, passwordHash, role string) (*UserRow, error) {
	const q = `
        INSERT INTO users (email, name, password_hash, role)
        VALUES ($1, $2, $3, $4)
        RETURNING id, email, name, password_hash, role, created_at
    `
	var u UserRow
	err := s.pool.QueryRow(ctx, q, email, name, passwordHash, role).Scan(
		&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.Role, &u.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("auth.UserStore.CreateWithRole: %w", err)
	}
	return &u, nil
}

func (s *UserStore) GetByEmail(ctx context.Context, email string) (*UserRow, error) {
	const q = `SELECT id, email, name, password_hash, role, created_at FROM users WHERE email = $1`
	var u UserRow
	err := s.pool.QueryRow(ctx, q, email).Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.Role, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("auth.UserStore.GetByEmail: %w", err)
	}
	return &u, nil
}

func (s *UserStore) GetByID(ctx context.Context, id string) (*UserRow, error) {
	const q = `SELECT id, email, name, password_hash, role, created_at FROM users WHERE id = $1`
	var u UserRow
	err := s.pool.QueryRow(ctx, q, id).Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.Role, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("auth.UserStore.GetByID: %w", err)
	}
	return &u, nil
}

func (s *UserStore) Count(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("auth.UserStore.Count: %w", err)
	}
	return n, nil
}
