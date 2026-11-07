package settings

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/skael-dev/skael/internal/audit"
)

// Channel is the NOTIFY channel a save signals on.
const Channel = "skael_settings"

// Keys seals and opens secrets. platform.Keyring implements it.
type Keys interface {
	Opener
	Seal(plain []byte, aad string) (string, error)
	Enabled() bool
}

// Store reads and writes the settings table.
type Store struct {
	pool *pgxpool.Pool
	keys Keys
}

// NewStore returns a store that seals secrets with keys.
func NewStore(pool *pgxpool.Pool, keys Keys) *Store { return &Store{pool: pool, keys: keys} }

// Version returns the current settings version.
func (s *Store) Version(ctx context.Context) (int64, error) {
	var v int64
	if err := s.pool.QueryRow(ctx, `SELECT version FROM settings_version WHERE id = 1`).Scan(&v); err != nil {
		return 0, fmt.Errorf("settings: version: %w", err)
	}
	return v, nil
}

// Load returns every stored row and the version they belong to.
func (s *Store) Load(ctx context.Context) (map[string]Stored, int64, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, 0, fmt.Errorf("settings: load: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var version int64
	if err := tx.QueryRow(ctx, `SELECT version FROM settings_version WHERE id = 1`).Scan(&version); err != nil {
		return nil, 0, fmt.Errorf("settings: load version: %w", err)
	}
	rows, err := tx.Query(ctx, `SELECT key, value, sealed, updated_by, updated_at FROM settings`)
	if err != nil {
		return nil, 0, fmt.Errorf("settings: load: %w", err)
	}
	defer rows.Close()
	out := map[string]Stored{}
	for rows.Next() {
		var key string
		var st Stored
		if err := rows.Scan(&key, &st.Value, &st.Sealed, &st.UpdatedBy, &st.UpdatedAt); err != nil {
			return nil, 0, fmt.Errorf("settings: load: %w", err)
		}
		out[key] = st
	}
	return out, version, rows.Err()
}

// SettingsChange is one edit. Exactly one of Value, Reference and Clear applies.
type SettingsChange struct {
	Key       string  `json:"key"`
	Value     *string `json:"value,omitempty"`
	Reference *string `json:"reference,omitempty"`
	Clear     bool    `json:"clear,omitempty"`
}

// ErrInvalid is a change the registry refuses. Its message is for the user.
type ErrInvalid struct{ Msg string }

func (e *ErrInvalid) Error() string { return e.Msg }

func invalid(format string, a ...any) error { return &ErrInvalid{Msg: fmt.Sprintf(format, a...)} }

// ErrNoKeys means a secret literal cannot be sealed.
var ErrNoKeys = errors.New("no master key can seal a secret; use an env: or file: reference")

// Check validates changes against the registry and the environment.
func Check(changes []SettingsChange, env EnvFunc) error {
	if len(changes) == 0 {
		return invalid("no change")
	}
	seen := map[string]bool{}
	for _, c := range changes {
		d := Lookup(c.Key)
		if d == nil {
			return invalid("unknown setting %q", c.Key)
		}
		if seen[c.Key] {
			return invalid("%s appears twice", d.Label)
		}
		seen[c.Key] = true
		if d.EnvOnly {
			return invalid("%s is set only through %s", d.Label, d.Env)
		}
		if v, ok := env(d.Env); ok && v != "" {
			return invalid("%s is set by %s, which wins over the UI", d.Label, d.Env)
		}
		n := 0
		if c.Value != nil {
			n++
		}
		if c.Reference != nil {
			n++
		}
		if c.Clear {
			n++
		}
		if n != 1 {
			return invalid("%s needs exactly one of value, reference or clear", d.Label)
		}
		if c.Reference != nil {
			if d.Kind != KindSecret {
				return invalid("%s is not a secret and takes no reference", d.Label)
			}
			if err := ValidReference(*c.Reference); err != nil {
				return invalid("%s: %s", d.Label, err.Error())
			}
		}
		if c.Value != nil {
			if *c.Value == "" {
				return invalid("%s: an empty value clears the setting; send clear", d.Label)
			}
			if err := d.Validate(*c.Value); err != nil {
				return invalid("%s", err.Error())
			}
		}
	}
	return nil
}

// Apply writes changes in one transaction, records each in the audit log,
// bumps the version and notifies every replica.
func (s *Store) Apply(ctx context.Context, actor string, changes []SettingsChange, env EnvFunc) error {
	if err := Check(changes, env); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("settings: apply: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	for _, c := range changes {
		d := Lookup(c.Key)
		detail := map[string]any{"key": c.Key}
		switch {
		case c.Clear:
			if _, err := tx.Exec(ctx, `DELETE FROM settings WHERE key = $1`, c.Key); err != nil {
				return fmt.Errorf("settings: clear %s: %w", c.Key, err)
			}
			detail["cleared"] = true
		case c.Reference != nil:
			if err := upsert(ctx, tx, c.Key, *c.Reference, "", actor); err != nil {
				return err
			}
			detail["reference"] = *c.Reference
		case d.Kind == KindSecret:
			if s.keys == nil || !s.keys.Enabled() {
				return ErrNoKeys
			}
			sealed, err := s.keys.Seal([]byte(*c.Value), aad(c.Key))
			if err != nil {
				return fmt.Errorf("settings: seal %s: %w", c.Key, err)
			}
			if err := upsert(ctx, tx, c.Key, "", sealed, actor); err != nil {
				return err
			}
			detail["secret"] = "set"
		default:
			if err := upsert(ctx, tx, c.Key, *c.Value, "", actor); err != nil {
				return err
			}
			detail["value"] = *c.Value
		}
		if err := audit.Record(ctx, tx, actor, "settings.update", "setting:"+c.Key, detail); err != nil {
			return err
		}
	}
	var version int64
	if err := tx.QueryRow(ctx, `UPDATE settings_version SET version = version + 1 WHERE id = 1 RETURNING version`).Scan(&version); err != nil {
		return fmt.Errorf("settings: bump version: %w", err)
	}
	if _, err := tx.Exec(ctx, `SELECT pg_notify($1, $2)`, Channel, strconv.FormatInt(version, 10)); err != nil {
		return fmt.Errorf("settings: notify: %w", err)
	}
	return tx.Commit(ctx)
}

func upsert(ctx context.Context, tx pgx.Tx, key, value, sealed, actor string) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO settings (key, value, sealed, updated_by, updated_at) VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, sealed = EXCLUDED.sealed,
			updated_by = EXCLUDED.updated_by, updated_at = now()`, key, value, sealed, actor)
	if err != nil {
		return fmt.Errorf("settings: write %s: %w", key, err)
	}
	return nil
}
