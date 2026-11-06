package gitsync

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Contest settings of a connection.
const (
	ContestOff       = "off"
	ContestOnRequest = "on_request"
	ContestReady     = "ready"
)

// Connection is one repo, branch, root path and name prefix whose skill
// folders Skael syncs.
type Connection struct {
	ID               string     `json:"id"`
	InstallationID   int64      `json:"installation_id"`
	RepoOwner        string     `json:"repo_owner"`
	RepoName         string     `json:"repo_name"`
	Branch           string     `json:"branch"`
	RootPath         string     `json:"root_path"`
	Prefix           string     `json:"prefix"`
	ContestMode      string     `json:"contest_mode"`
	ServiceAccountID string     `json:"service_account_id"`
	CreatedBy        string     `json:"created_by"`
	CreatedAt        time.Time  `json:"created_at"`
	Status           string     `json:"status"`
	LastError        string     `json:"last_error,omitempty"`
	LastSyncedSHA    string     `json:"last_synced_sha,omitempty"`
	LastSyncAt       *time.Time `json:"last_sync_at,omitempty"`
	LastWebhookAt    *time.Time `json:"last_webhook_at,omitempty"`
}

// Repo names the connection's repository as owner/name.
func (c *Connection) Repo() string { return c.RepoOwner + "/" + c.RepoName }

// Binding is one git-bound skill.
type Binding struct {
	SkillID      string     `json:"skill_id"`
	SkillName    string     `json:"skill_name"`
	ConnectionID string     `json:"connection_id"`
	Path         string     `json:"path"`
	LastCommit   string     `json:"last_commit,omitempty"`
	RemovedAt    *time.Time `json:"removed_from_git_at,omitempty"`
}

// Refusal is a folder the last sync did not take, and why.
type Refusal struct {
	Path      string `json:"path"`
	SkillName string `json:"skill_name"`
	Reason    string `json:"reason"`
}

const connectionColumns = `id, installation_id, repo_owner, repo_name, branch, root_path, prefix,
	contest_mode, service_account_id, created_by, created_at, status, last_error, last_synced_sha,
	last_sync_at, last_webhook_at`

func scanConnection(r interface{ Scan(...any) error }) (*Connection, error) {
	var c Connection
	err := r.Scan(&c.ID, &c.InstallationID, &c.RepoOwner, &c.RepoName, &c.Branch, &c.RootPath, &c.Prefix,
		&c.ContestMode, &c.ServiceAccountID, &c.CreatedBy, &c.CreatedAt, &c.Status, &c.LastError,
		&c.LastSyncedSHA, &c.LastSyncAt, &c.LastWebhookAt)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// Connections returns every connection, by repo.
func (s *Store) Connections(ctx context.Context) ([]Connection, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+connectionColumns+` FROM git_connections
		ORDER BY repo_owner, repo_name, branch, root_path`)
	if err != nil {
		return nil, fmt.Errorf("gitsync.Store.Connections: %w", err)
	}
	defer rows.Close()
	out := []Connection{}
	for rows.Next() {
		c, err := scanConnection(rows)
		if err != nil {
			return nil, fmt.Errorf("gitsync.Store.Connections: %w", err)
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// Connection returns one connection, or (nil, nil).
func (s *Store) Connection(ctx context.Context, id string) (*Connection, error) {
	c, err := scanConnection(s.pool.QueryRow(ctx, `SELECT `+connectionColumns+` FROM git_connections WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("gitsync.Store.Connection: %w", err)
	}
	return c, nil
}

// ConnectionsForRepo returns the connections on owner/repo, for a webhook.
func (s *Store) ConnectionsForRepo(ctx context.Context, owner, repo string) ([]Connection, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+connectionColumns+` FROM git_connections
		WHERE lower(repo_owner) = lower($1) AND lower(repo_name) = lower($2)`, owner, repo)
	if err != nil {
		return nil, fmt.Errorf("gitsync.Store.ConnectionsForRepo: %w", err)
	}
	defer rows.Close()
	var out []Connection
	for rows.Next() {
		c, err := scanConnection(rows)
		if err != nil {
			return nil, fmt.Errorf("gitsync.Store.ConnectionsForRepo: %w", err)
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// ConnectionIDs returns every connection id, for the periodic sync.
func (s *Store) ConnectionIDs(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT id::text FROM git_connections ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("gitsync.Store.ConnectionIDs: %w", err)
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

const bindingSelect = `
	SELECT b.skill_id, s.name, b.connection_id, b.path, b.last_commit, s.removed_from_git_at
	FROM git_bindings b JOIN skills s ON s.id = b.skill_id`

func collectBindings(rows pgx.Rows) ([]Binding, error) {
	defer rows.Close()
	out := []Binding{}
	for rows.Next() {
		var b Binding
		if err := rows.Scan(&b.SkillID, &b.SkillName, &b.ConnectionID, &b.Path, &b.LastCommit, &b.RemovedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// Bindings returns a connection's git-bound skills, by name.
func (s *Store) Bindings(ctx context.Context, connectionID string) ([]Binding, error) {
	rows, err := s.pool.Query(ctx, bindingSelect+` WHERE b.connection_id = $1 ORDER BY s.name`, connectionID)
	if err != nil {
		return nil, fmt.Errorf("gitsync.Store.Bindings: %w", err)
	}
	out, err := collectBindings(rows)
	if err != nil {
		return nil, fmt.Errorf("gitsync.Store.Bindings: %w", err)
	}
	return out, nil
}

// BindingForSkill returns the binding of a skill name, or (nil, nil) for a
// native skill.
func (s *Store) BindingForSkill(ctx context.Context, name string) (*Binding, error) {
	rows, err := s.pool.Query(ctx, bindingSelect+` WHERE s.name = $1`, name)
	if err != nil {
		return nil, fmt.Errorf("gitsync.Store.BindingForSkill: %w", err)
	}
	out, err := collectBindings(rows)
	if err != nil {
		return nil, fmt.Errorf("gitsync.Store.BindingForSkill: %w", err)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return &out[0], nil
}

// Refusals returns what the last sync refused.
func (s *Store) Refusals(ctx context.Context, connectionID string) ([]Refusal, error) {
	rows, err := s.pool.Query(ctx, `SELECT path, skill_name, reason FROM git_connection_refusals
		WHERE connection_id = $1 ORDER BY path`, connectionID)
	if err != nil {
		return nil, fmt.Errorf("gitsync.Store.Refusals: %w", err)
	}
	defer rows.Close()
	out := []Refusal{}
	for rows.Next() {
		var r Refusal
		if err := rows.Scan(&r.Path, &r.SkillName, &r.Reason); err != nil {
			return nil, fmt.Errorf("gitsync.Store.Refusals: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Ignored returns the names a connection never adopts again.
func (s *Store) Ignored(ctx context.Context, connectionID string) (map[string]bool, error) {
	rows, err := s.pool.Query(ctx, `SELECT skill_name FROM git_connection_ignores WHERE connection_id = $1`, connectionID)
	if err != nil {
		return nil, fmt.Errorf("gitsync.Store.Ignored: %w", err)
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("gitsync.Store.Ignored: %w", err)
	}
	out := map[string]bool{}
	for _, n := range names {
		out[n] = true
	}
	return out, nil
}

// recordError marks a connection errored and changes no skill.
func (s *Store) recordError(ctx context.Context, id, reason string) error {
	_, err := s.pool.Exec(ctx, `UPDATE git_connections SET status = 'error', last_error = $2, last_sync_at = now()
		WHERE id = $1`, id, reason)
	if err != nil {
		return fmt.Errorf("gitsync.Store.recordError: %w", err)
	}
	return nil
}

// recordSync stores a finished sync and replaces the refusals.
func (s *Store) recordSync(ctx context.Context, id, sha string, refusals []Refusal) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("gitsync.Store.recordSync: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `UPDATE git_connections SET status = 'ok', last_error = '', last_synced_sha = $2,
		last_sync_at = now() WHERE id = $1`, id, sha); err != nil {
		return fmt.Errorf("gitsync.Store.recordSync: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM git_connection_refusals WHERE connection_id = $1`, id); err != nil {
		return fmt.Errorf("gitsync.Store.recordSync: %w", err)
	}
	for _, r := range refusals {
		if _, err := tx.Exec(ctx, `INSERT INTO git_connection_refusals (connection_id, path, skill_name, reason)
			VALUES ($1, $2, $3, $4) ON CONFLICT (connection_id, path) DO UPDATE SET reason = EXCLUDED.reason`,
			id, r.Path, r.SkillName, r.Reason); err != nil {
			return fmt.Errorf("gitsync.Store.recordSync: refusal: %w", err)
		}
	}
	return tx.Commit(ctx)
}

// TouchWebhook records that GitHub reached this instance for a connection.
func (s *Store) TouchWebhook(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `UPDATE git_connections SET last_webhook_at = now() WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("gitsync.Store.TouchWebhook: %w", err)
	}
	return nil
}
