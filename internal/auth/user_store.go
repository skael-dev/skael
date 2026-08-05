package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type UserRow struct {
	ID                    string
	Email                 string
	Name                  string
	PasswordHash          string
	Role                  string
	PasswordResetRequired bool
	CreatedAt             time.Time
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
        RETURNING id, email, name, password_hash, role, password_reset_required, created_at
    `
	var u UserRow
	err := s.pool.QueryRow(ctx, q, email, name, passwordHash).Scan(
		&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.Role, &u.PasswordResetRequired, &u.CreatedAt,
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
        RETURNING id, email, name, password_hash, role, password_reset_required, created_at
    `
	var u UserRow
	err := s.pool.QueryRow(ctx, q, email, name, passwordHash, role).Scan(
		&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.Role, &u.PasswordResetRequired, &u.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("auth.UserStore.CreateWithRole: %w", err)
	}
	return &u, nil
}

func (s *UserStore) GetByEmail(ctx context.Context, email string) (*UserRow, error) {
	const q = `SELECT id, email, name, password_hash, role, password_reset_required, created_at FROM users WHERE email = $1`
	var u UserRow
	err := s.pool.QueryRow(ctx, q, email).Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.Role, &u.PasswordResetRequired, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("auth.UserStore.GetByEmail: %w", err)
	}
	return &u, nil
}

func (s *UserStore) GetByID(ctx context.Context, id string) (*UserRow, error) {
	const q = `SELECT id, email, name, password_hash, role, password_reset_required, created_at FROM users WHERE id = $1`
	var u UserRow
	err := s.pool.QueryRow(ctx, q, id).Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.Role, &u.PasswordResetRequired, &u.CreatedAt)
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

func (s *UserStore) UpdatePassword(ctx context.Context, id, passwordHash string) error {
	const q = `UPDATE users SET password_hash = $2, password_reset_required = false WHERE id = $1`
	_, err := s.pool.Exec(ctx, q, id, passwordHash)
	return err
}

func (s *UserStore) SetResetRequired(ctx context.Context, id string, required bool) error {
	const q = `UPDATE users SET password_reset_required = $2 WHERE id = $1`
	_, err := s.pool.Exec(ctx, q, id, required)
	return err
}

// UpdateRole sets a user's role. It reports whether a row was updated so the
// caller can distinguish "no such user" from a successful change. Callers are
// responsible for authorising the change and for validating the role value.
func (s *UserStore) UpdateRole(ctx context.Context, id, role string) (bool, error) {
	const q = `UPDATE users SET role = $2 WHERE id = $1`
	tag, err := s.pool.Exec(ctx, q, id, role)
	if err != nil {
		return false, fmt.Errorf("auth.UserStore.UpdateRole: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

func (s *UserStore) List(ctx context.Context) ([]UserRow, error) {
	const q = `SELECT id, email, name, password_hash, role, password_reset_required, created_at FROM users ORDER BY created_at`
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("auth.UserStore.List: %w", err)
	}
	defer rows.Close()

	var users []UserRow
	for rows.Next() {
		var u UserRow
		if err := rows.Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.Role, &u.PasswordResetRequired, &u.CreatedAt); err != nil {
			return nil, fmt.Errorf("auth.UserStore.List: scan: %w", err)
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// Search finds users by name or email prefix, case-insensitively. It is
// deliberately capped and requires a minimum query length: the endpoint on
// top of it is open to every authenticated member, so it must be a lookup
// rather than a way to dump the directory.
func (s *UserStore) Search(ctx context.Context, q string, limit int) ([]UserRow, error) {
	const minQuery = 2
	trimmed := strings.TrimSpace(q)
	if len([]rune(trimmed)) < minQuery {
		return []UserRow{}, nil
	}
	if limit <= 0 || limit > 20 {
		limit = 20
	}
	const sqlQuery = `
		SELECT id, email, name, role, created_at
		FROM users
		WHERE name ILIKE $1 OR email ILIKE $1
		ORDER BY name
		LIMIT $2`
	rows, err := s.pool.Query(ctx, sqlQuery, trimmed+"%", limit)
	if err != nil {
		return nil, fmt.Errorf("auth.UserStore.Search: %w", err)
	}
	defer rows.Close()

	users := []UserRow{}
	for rows.Next() {
		var u UserRow
		if err := rows.Scan(&u.ID, &u.Email, &u.Name, &u.Role, &u.CreatedAt); err != nil {
			return nil, fmt.Errorf("auth.UserStore.Search: scan: %w", err)
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("auth.UserStore.Search: %w", err)
	}
	return users, nil
}
