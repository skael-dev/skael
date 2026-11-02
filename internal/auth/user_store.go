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

// UserRow is one auth_users row. auth-all writes people, and Skael reads them.
type UserRow struct {
	ID                 string
	Email              string
	Name               string
	Role               string
	MustChangePassword bool
	DisabledAt         *time.Time
	CreatedAt          time.Time
	Kind               string
}

const userColumns = `id, email, display_name, role, must_change_password, disabled_at, created_at, kind`

func scanUser(row pgx.Row) (*UserRow, error) {
	var u UserRow
	err := row.Scan(&u.ID, &u.Email, &u.Name, &u.Role, &u.MustChangePassword, &u.DisabledAt, &u.CreatedAt, &u.Kind)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

type UserStore struct {
	pool *pgxpool.Pool
}

func NewUserStore(pool *pgxpool.Pool) *UserStore {
	return &UserStore{pool: pool}
}

// Create inserts a member with no credential.
func (s *UserStore) Create(ctx context.Context, email, name string) (*UserRow, error) {
	return s.CreateWithRole(ctx, email, name, RoleMember)
}

// CreateWithRole inserts a user with no credential.
func (s *UserStore) CreateWithRole(ctx context.Context, email, name, role string) (*UserRow, error) {
	q := `INSERT INTO auth_users (id, email, email_normalized, display_name, role, created_at)
        VALUES (gen_random_uuid(), $1, lower(trim($1)), $2, $3, now())
        RETURNING ` + userColumns
	u, err := scanUser(s.pool.QueryRow(ctx, q, email, name, role))
	if err != nil {
		return nil, fmt.Errorf("auth.UserStore.CreateWithRole: %w", err)
	}
	return u, nil
}

func (s *UserStore) GetByEmail(ctx context.Context, email string) (*UserRow, error) {
	u, err := scanUser(s.pool.QueryRow(ctx, `SELECT `+userColumns+` FROM auth_users WHERE email_normalized = lower(trim($1))`, email))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("auth.UserStore.GetByEmail: %w", err)
	}
	return u, nil
}

func (s *UserStore) GetByID(ctx context.Context, id string) (*UserRow, error) {
	u, err := scanUser(s.pool.QueryRow(ctx, `SELECT `+userColumns+` FROM auth_users WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("auth.UserStore.GetByID: %w", err)
	}
	return u, nil
}

func (s *UserStore) Count(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM auth_users WHERE kind = 'human'`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("auth.UserStore.Count: %w", err)
	}
	return n, nil
}

func (s *UserStore) List(ctx context.Context) ([]UserRow, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+userColumns+` FROM auth_users WHERE kind = 'human' ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("auth.UserStore.List: %w", err)
	}
	defer rows.Close()

	var users []UserRow
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, fmt.Errorf("auth.UserStore.List: scan: %w", err)
		}
		users = append(users, *u)
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
		SELECT id, email, display_name, role, created_at
		FROM auth_users
		WHERE kind = 'human' AND (display_name ILIKE $1 OR email ILIKE $1)
		ORDER BY display_name
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
