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
// Only the tables the current code reads are created here; more are added
// alongside the code that reads them (runs, scores, and so on).
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

	`CREATE TABLE evals (
		id             INTEGER PRIMARY KEY AUTOINCREMENT,
		skill_name     TEXT    NOT NULL,
		spec_version   INTEGER NOT NULL DEFAULT 0,
		tier           TEXT    NOT NULL,
		suite_ref      TEXT    NOT NULL DEFAULT '',
		engine_version TEXT    NOT NULL DEFAULT '',
		model_panel    TEXT    NOT NULL DEFAULT '[]',
		seed           INTEGER NOT NULL DEFAULT 0,
		started_at     TEXT    NOT NULL,
		finished_at    TEXT,
		status         TEXT    NOT NULL
	);
	CREATE INDEX idx_evals_skill ON evals (skill_name, id DESC);

	CREATE TABLE runs (
		id            INTEGER PRIMARY KEY AUTOINCREMENT,
		eval_id       INTEGER NOT NULL REFERENCES evals(id),
		task_id       TEXT    NOT NULL,
		agent         TEXT    NOT NULL,
		model         TEXT    NOT NULL,
		condition     TEXT    NOT NULL,
		attempt       INTEGER NOT NULL,
		artifact_dir  TEXT    NOT NULL DEFAULT '',
		verifier_exit INTEGER,
		input_tokens  INTEGER NOT NULL DEFAULT 0,
		output_tokens INTEGER NOT NULL DEFAULT 0,
		duration_ms   INTEGER NOT NULL DEFAULT 0,
		agent_version TEXT    NOT NULL DEFAULT '',
		rate_limited  INTEGER NOT NULL DEFAULT 0,
		status        TEXT    NOT NULL DEFAULT 'claimed',
		error         TEXT    NOT NULL DEFAULT '',
		created_at    TEXT    NOT NULL DEFAULT (datetime('now')),
		-- The resume mechanism. A key that already has a finished row is
		-- skipped; one with a claimed-but-unfinished row is retried, because a
		-- process killed mid-run must not silently drop a session from a
		-- denominator.
		UNIQUE (eval_id, task_id, agent, model, condition, attempt)
	);
	CREATE INDEX idx_runs_eval ON runs (eval_id);

	CREATE TABLE judgments (
		id       INTEGER PRIMARY KEY AUTOINCREMENT,
		eval_id  INTEGER NOT NULL REFERENCES evals(id),
		task_id  TEXT    NOT NULL DEFAULT '',
		model    TEXT    NOT NULL DEFAULT '',
		kind     TEXT    NOT NULL,
		rule_id  TEXT    NOT NULL DEFAULT '',
		winner   TEXT    NOT NULL DEFAULT '',
		margin   REAL    NOT NULL DEFAULT 0,
		evidence TEXT    NOT NULL DEFAULT '',
		votes    INTEGER NOT NULL DEFAULT 1,
		swapped  INTEGER NOT NULL DEFAULT 0,
		created_at TEXT  NOT NULL DEFAULT (datetime('now'))
	);
	CREATE INDEX idx_judgments_eval ON judgments (eval_id);

	CREATE TABLE scores (
		eval_id       INTEGER NOT NULL REFERENCES evals(id),
		agent         TEXT    NOT NULL,
		model         TEXT    NOT NULL,
		trigger_f1    REAL    NOT NULL DEFAULT 0,
		reliability   REAL    NOT NULL DEFAULT 0,
		uplift        REAL    NOT NULL DEFAULT 0,
		efficiency    REAL    NOT NULL DEFAULT 0,
		effectiveness REAL    NOT NULL DEFAULT 0,
		adherence     REAL    NOT NULL DEFAULT 0,
		drift         REAL    NOT NULL DEFAULT 0,
		grade         TEXT    NOT NULL DEFAULT '',
		PRIMARY KEY (eval_id, agent, model)
	);

	CREATE TABLE reports (
		eval_id        INTEGER PRIMARY KEY REFERENCES evals(id),
		doc            TEXT    NOT NULL,
		headline       REAL    NOT NULL DEFAULT 0,
		panel_complete INTEGER NOT NULL DEFAULT 0,
		robustness_gap REAL    NOT NULL DEFAULT 0,
		created_at     TEXT    NOT NULL DEFAULT (datetime('now'))
	);

	CREATE TABLE suite_checks (
		skill_name TEXT NOT NULL,
		suite_ref  TEXT NOT NULL,
		task_id    TEXT NOT NULL,
		void       INTEGER NOT NULL DEFAULT 0,
		reason     TEXT NOT NULL DEFAULT '',
		checked_at TEXT NOT NULL DEFAULT (datetime('now')),
		PRIMARY KEY (skill_name, suite_ref, task_id)
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
