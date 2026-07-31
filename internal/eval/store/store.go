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

	// _pragma settings are driver DSN options: foreign keys on, and WAL so a
	// reader (a report) does not block a writer (a run in progress).
	// _txlock=immediate takes the write lock at BEGIN rather than at the first
	// write. A deferred transaction (the driver default) opens its read
	// snapshot at BEGIN and only upgrades to a writer on its first write
	// statement; if another writer commits in between, SQLite invalidates
	// that snapshot outright (SQLITE_BUSY_SNAPSHOT) instead of retrying it —
	// busy_timeout cannot help because there is no lock to wait out, only a
	// stale snapshot. SaveSpec reads (MAX(version)) before it writes, so it
	// hits exactly this shape; immediate mode takes the write lock up front
	// and serializes concurrent SaveSpec calls instead of aborting one.
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
//
// Every path helper below eventually calls this before touching
// filepath.Join, because a skill name may arrive from a GitHub import or an
// unpacked archive rather than a validated spec, and this package treats
// that input as untrusted: filepath.Join("skills", "../../etc") escapes the
// workspace, and both "" and a lone ":" collapse to the "skills" directory
// itself — the shared parent of every skill. The check reuses
// spec.SkillSpec.Validate's name rule (the same one an authored spec is held
// to) rather than a second, hand-rolled pattern that could drift from it. The
// other probe fields are filled with values guaranteed to pass validation, so
// any error Validate returns here is necessarily about the name.
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

// SpecPath is the human-editable spec YAML. It sits beside the bundle rather
// than inside the eval sidecar because the spec is the authored artifact, not
// eval scaffolding.
func (s *Store) SpecPath(skill string) (string, error) {
	dir, err := s.SkillDir(skill)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "spec.yaml"), nil
}

// EvalDir is the sidecar directory. lint.Excluded is the one definition of
// what does not ship as bundle content; everything eval-only must live under
// this path so pack's exclusion of it stays correct.
func (s *Store) EvalDir(skill string) (string, error) {
	dir, err := s.SkillDir(skill)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, lint.SidecarDir), nil
}

// ContractPath is the compiled drift contract.
func (s *Store) ContractPath(skill string) (string, error) {
	dir, err := s.EvalDir(skill)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "contract.yaml"), nil
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
