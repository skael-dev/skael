package store

import (
	"database/sql"
	"fmt"
)

// migrations are applied in order; the applied count is tracked in
// PRAGMA user_version. goose is deliberately not used here — the repo's goose
// setup targets Postgres for the server, and a second dialect in that path
// would confuse the server's migration story for no benefit.
//
// Only tables this phase reads are created. Runs and scores arrive with the
// code that produces them.
var migrations = []string{
	`CREATE TABLE specs (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		skill_name  TEXT    NOT NULL,
		version     INTEGER NOT NULL,
		yaml        TEXT    NOT NULL,
		approved    INTEGER NOT NULL DEFAULT 0,
		created_at  TEXT    NOT NULL DEFAULT (datetime('now')),
		UNIQUE (skill_name, version)
	);
	CREATE INDEX idx_specs_skill ON specs (skill_name, version DESC);`,

	`CREATE TABLE llm_cache (
		key        TEXT PRIMARY KEY,
		value      TEXT NOT NULL,
		created_at TEXT NOT NULL DEFAULT (datetime('now'))
	);`,
}

func migrate(db *sql.DB) error {
	var current int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&current); err != nil {
		return fmt.Errorf("store.migrate read user_version: %w", err)
	}
	if current > len(migrations) {
		return fmt.Errorf("store.migrate: database is at version %d but this build only knows %d — "+
			"it was written by a newer whetstone", current, len(migrations))
	}

	for i := current; i < len(migrations); i++ {
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("store.migrate begin %d: %w", i+1, err)
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("store.migrate apply %d: %w", i+1, err)
		}
		// PRAGMA does not accept a bound parameter.
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, i+1)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("store.migrate stamp %d: %w", i+1, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("store.migrate commit %d: %w", i+1, err)
		}
	}
	return nil
}
