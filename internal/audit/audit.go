// Package audit records who changed what.
package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Execer lets Record join the transaction that made the change, so a change
// and its record commit together.
type Execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// AuditEntry is one audit record.
type AuditEntry struct {
	ID        string          `json:"id"`
	Actor     string          `json:"actor"`
	Action    string          `json:"action"`
	Resource  string          `json:"resource"`
	Detail    json.RawMessage `json:"detail"`
	CreatedAt time.Time       `json:"created_at"`
}

// Record writes one entry through e. A nil detail stores an empty object.
func Record(ctx context.Context, e Execer, actor, action, resource string, detail any) error {
	raw := []byte("{}")
	if detail != nil {
		var err error
		if raw, err = json.Marshal(detail); err != nil {
			return fmt.Errorf("audit.Record: marshal: %w", err)
		}
	}
	const q = `INSERT INTO audit_log (actor, action, resource, detail) VALUES ($1, $2, $3, $4)`
	if _, err := e.Exec(ctx, q, actor, action, resource, raw); err != nil {
		return fmt.Errorf("audit.Record: %w", err)
	}
	return nil
}

// List returns the newest entries first.
func List(ctx context.Context, pool *pgxpool.Pool, limit int) ([]AuditEntry, error) {
	const q = `SELECT id, actor, action, resource, detail, created_at FROM audit_log ORDER BY created_at DESC LIMIT $1`
	rows, err := pool.Query(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("audit.List: %w", err)
	}
	entries, err := pgx.CollectRows(rows, pgx.RowToStructByPos[AuditEntry])
	if err != nil {
		return nil, fmt.Errorf("audit.List: %w", err)
	}
	return entries, nil
}
