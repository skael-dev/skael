package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// cache implements llm.Cache over the llm_cache table.
type cache struct{ db *sql.DB }

// Get returns a cached completion.
func (c *cache) Get(key string) (string, bool, error) {
	var v string
	err := c.db.QueryRow(`SELECT value FROM llm_cache WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("store cache get: %w", err)
	}
	return v, true, nil
}

// Put stores a completion, overwriting any existing entry. Upsert rather than
// insert: a re-run recomputes the same content hash, and a unique-constraint
// failure there would abort the run.
func (c *cache) Put(key, value string) error {
	_, err := c.db.Exec(
		`INSERT INTO llm_cache (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	if err != nil {
		return fmt.Errorf("store cache put: %w", err)
	}
	return nil
}
