// Package store owns the .whetstone/ workspace: SQLite index, completion
// cache, and the on-disk artifact layout. Uses modernc.org/sqlite (pure Go)
// because the release build is CGO_ENABLED=0.
package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"

	"github.com/skael-dev/skael/internal/eval/lint"
	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/eval/spec"
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

	// WAL lets readers not block writers. _txlock=immediate prevents
	// SQLITE_BUSY_SNAPSHOT: a deferred transaction's read snapshot is
	// invalidated if another writer commits between BEGIN and the first write.
	dsn := "file:" + filepath.Join(base, "whetstone.db") +
		"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_txlock=immediate"

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

// skillDirName validates skill and returns its on-disk directory component.
// Reuses spec.SkillSpec.Validate rather than a separate pattern that could
// drift. Every path helper below calls this before filepath.Join.
func skillDirName(skill string) (string, error) {
	probe := spec.SkillSpec{
		Name:        skill,
		Purpose:     "probe",
		Description: "probe",
		Triggers:    []spec.TriggerPhrase{{Text: "probe"}},
		Steps:       []spec.Step{{ID: "s1", Action: "probe", Postcondition: "probe"}},
		TargetTier:  spec.TierMid,
	}
	if errs := probe.Validate(); len(errs) > 0 {
		return "", fmt.Errorf("store: invalid skill name %q: %w", skill, errs[0])
	}
	return probe.DirName(), nil
}

// SkillDir is the bundle root for a skill. It returns an error rather than a
// path when skill is not a legal spec name — see skillDirName.
func (s *Store) SkillDir(skill string) (string, error) {
	dir, err := skillDirName(skill)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.root, "skills", dir), nil
}

// SpecPath is the spec YAML, beside the bundle rather than in the sidecar.
func (s *Store) SpecPath(skill string) (string, error) {
	dir, err := s.SkillDir(skill)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "spec.yaml"), nil
}

// EvalDir is the eval sidecar directory.
func (s *Store) EvalDir(skill string) (string, error) {
	dir, err := s.SkillDir(skill)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, lint.SidecarDir), nil
}

// SuiteDir holds generated task packages and the trigger set.
func (s *Store) SuiteDir(skill string) (string, error) {
	dir, err := s.EvalDir(skill)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "suite"), nil
}

// Cache returns the completion cache.
func (s *Store) Cache() llm.Cache { return &cache{db: s.db} }
