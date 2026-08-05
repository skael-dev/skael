package ownership

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the Postgres side of ownership. The decision logic stays in
// rule.go and manage.go, which are pure; this only loads and writes.
type Store struct{ pool *pgxpool.Pool }

// NewStore builds a Store over pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// ErrNoMembers is returned when a rule would be written with nobody on it. A
// memberless rule resolves a name to "owned" with no one able to review it —
// strictly worse than unowned, which at least falls back to instance admins.
var ErrNoMembers = errors.New("ownership: a rule must have at least one member")

// List returns every rule with its members, ordered by pattern.
func (s *Store) List(ctx context.Context) ([]Rule, error) {
	const q = `
		SELECT r.id, r.pattern,
		       COALESCE(array_agg(m.user_id::text ORDER BY m.added_at)
		                FILTER (WHERE m.user_id IS NOT NULL), '{}') AS members
		FROM ownership_rules r
		LEFT JOIN ownership_rule_members m ON m.rule_id = r.id
		GROUP BY r.id, r.pattern
		ORDER BY r.pattern`

	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("ownership.Store.List: %w", err)
	}
	defer rows.Close()

	rules := []Rule{}
	for rows.Next() {
		var r Rule
		if err := rows.Scan(&r.ID, &r.Pattern, &r.Members); err != nil {
			return nil, fmt.Errorf("ownership.Store.List scan: %w", err)
		}
		rules = append(rules, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ownership.Store.List rows: %w", err)
	}
	return rules, nil
}

// GetByPattern returns the rule for pattern, or (nil, nil) if none exists.
func (s *Store) GetByPattern(ctx context.Context, pattern string) (*Rule, error) {
	rules, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	for i := range rules {
		if rules[i].Pattern == pattern {
			return &rules[i], nil
		}
	}
	return nil, nil
}

// Upsert creates the rule for pattern or replaces its member list wholesale.
// Replace, not merge: `skael owners set` and PUT both promise the list they
// were given is the list that results, and a merge would make removing
// someone impossible through the primary verb.
func (s *Store) Upsert(ctx context.Context, pattern string, memberIDs []string, createdBy string) (*Rule, error) {
	if err := ValidatePattern(pattern); err != nil {
		return nil, err
	}
	if len(memberIDs) == 0 {
		return nil, ErrNoMembers
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("ownership.Store.Upsert begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var by any
	if createdBy != "" {
		by = createdBy
	}

	var id string
	const up = `
		INSERT INTO ownership_rules (pattern, created_by)
		VALUES ($1, $2)
		ON CONFLICT (pattern) DO UPDATE SET updated_at = now()
		RETURNING id`
	if err := tx.QueryRow(ctx, up, pattern, by).Scan(&id); err != nil {
		return nil, fmt.Errorf("ownership.Store.Upsert rule: %w", err)
	}

	if _, err := tx.Exec(ctx, `DELETE FROM ownership_rule_members WHERE rule_id = $1`, id); err != nil {
		return nil, fmt.Errorf("ownership.Store.Upsert clear members: %w", err)
	}
	for _, uid := range memberIDs {
		const ins = `
			INSERT INTO ownership_rule_members (rule_id, user_id, added_by)
			VALUES ($1, $2, $3)
			ON CONFLICT DO NOTHING`
		if _, err := tx.Exec(ctx, ins, id, uid, by); err != nil {
			return nil, fmt.Errorf("ownership.Store.Upsert add member %s: %w", uid, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("ownership.Store.Upsert commit: %w", err)
	}
	return s.GetByPattern(ctx, pattern)
}

// Delete removes a rule by id, reporting whether a row was removed. Members
// cascade. Deleting a rule never touches any published version (O10): it
// changes who reviews future changes and nothing else.
func (s *Store) Delete(ctx context.Context, id string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM ownership_rules WHERE id = $1`, id)
	if err != nil {
		// An invalid UUID is a caller error, not a server fault.
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("ownership.Store.Delete: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}
