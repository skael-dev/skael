// Package store owns the .whetstone/ workspace: a SQLite index for versioned
// specs and the completion cache, plus the on-disk artifact layout.
//
// The SQLite driver is modernc.org/sqlite (pure Go). The release build is
// CGO_ENABLED=0 across five platform pairs, so a cgo driver is not an option.
package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/skael-dev/skael/internal/eval/llm"
)

// dirName is the workspace directory created inside the project root.
const dirName = ".whetstone"

// Store is an open .whetstone/ workspace.
type Store struct {
	root string
	db   *sql.DB
}

// Open creates or opens the workspace under root and applies pending
// migrations.
func Open(root string) (*Store, error) {
	base := filepath.Join(root, dirName)
	if err := os.MkdirAll(filepath.Join(base, "skills"), 0o755); err != nil {
		return nil, fmt.Errorf("store.Open mkdir: %w", err)
	}

	// _pragma settings are driver DSN options: foreign keys on, and WAL so a
	// reader (a report) does not block a writer (a run in progress).
	dsn := "file:" + filepath.Join(base, "whetstone.db") +
		"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store.Open: %w", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store.Open ping: %w", err)
	}
	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{root: base, db: db}, nil
}

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

// Root is the .whetstone directory.
func (s *Store) Root() string { return s.root }

// dirNameFor strips a namespace prefix so a registry name like
// "superpowers:brainstorming" yields a spec-legal directory.
func dirNameFor(skill string) string {
	if i := strings.LastIndex(skill, ":"); i >= 0 {
		return skill[i+1:]
	}
	return skill
}

// SkillDir is the bundle root for a skill.
func (s *Store) SkillDir(skill string) string {
	return filepath.Join(s.root, "skills", dirNameFor(skill))
}

// SpecPath is the human-editable spec YAML. It sits beside the bundle rather
// than inside the eval sidecar because the spec is the authored artifact, not
// eval scaffolding.
func (s *Store) SpecPath(skill string) string {
	return filepath.Join(s.SkillDir(skill), "spec.yaml")
}

// EvalDir is the sidecar directory. `pack` removes exactly this path, so
// everything eval-only must live under it.
func (s *Store) EvalDir(skill string) string {
	return filepath.Join(s.SkillDir(skill), "eval")
}

// ContractPath is the compiled drift contract.
func (s *Store) ContractPath(skill string) string {
	return filepath.Join(s.EvalDir(skill), "contract.yaml")
}

// SuiteDir holds generated task packages and the trigger set.
func (s *Store) SuiteDir(skill string) string {
	return filepath.Join(s.EvalDir(skill), "suite")
}

// Cache returns the completion cache.
func (s *Store) Cache() llm.Cache { return &cache{db: s.db} }
