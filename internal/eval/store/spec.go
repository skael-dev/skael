package store

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/skael-dev/skael/internal/eval/spec"
)

// SpecRecord is one entry in a skill's spec history.
type SpecRecord struct {
	Version   int
	Approved  bool
	CreatedAt time.Time
}

// SaveSpec appends a new spec version and rewrites the human-editable YAML.
// Both, deliberately: the database gives history and approval state, the file
// gives `whetstone spec edit` something to open.
func (s *Store) SaveSpec(sp *spec.SkillSpec) (int, error) {
	var buf bytes.Buffer
	if err := sp.Save(&buf); err != nil {
		return 0, err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("store.SaveSpec begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var next int
	err = tx.QueryRow(`SELECT COALESCE(MAX(version), 0) + 1 FROM specs WHERE skill_name = ?`, sp.Name).Scan(&next)
	if err != nil {
		return 0, fmt.Errorf("store.SaveSpec next version: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO specs (skill_name, version, yaml) VALUES (?, ?, ?)`,
		sp.Name, next, buf.String()); err != nil {
		return 0, fmt.Errorf("store.SaveSpec insert: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store.SaveSpec commit: %w", err)
	}

	path := s.SpecPath(sp.Name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return 0, fmt.Errorf("store.SaveSpec mkdir: %w", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return 0, fmt.Errorf("store.SaveSpec write yaml: %w", err)
	}
	return next, nil
}

// LoadSpec returns the latest stored spec version for a skill.
func (s *Store) LoadSpec(skill string) (*spec.SkillSpec, int, error) {
	var (
		raw     string
		version int
	)
	err := s.db.QueryRow(
		`SELECT yaml, version FROM specs WHERE skill_name = ? ORDER BY version DESC LIMIT 1`,
		skill).Scan(&raw, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, fmt.Errorf("store.LoadSpec: no spec stored for %q", skill)
	}
	if err != nil {
		return nil, 0, fmt.Errorf("store.LoadSpec: %w", err)
	}

	sp, err := spec.Load(bytes.NewReader([]byte(raw)))
	if err != nil {
		return nil, 0, err
	}
	return sp, version, nil
}

// SpecHistory lists every stored version, newest first.
func (s *Store) SpecHistory(skill string) ([]SpecRecord, error) {
	rows, err := s.db.Query(
		`SELECT version, approved, created_at FROM specs WHERE skill_name = ? ORDER BY version DESC`, skill)
	if err != nil {
		return nil, fmt.Errorf("store.SpecHistory: %w", err)
	}
	defer rows.Close()

	var out []SpecRecord
	for rows.Next() {
		var (
			r  SpecRecord
			ts string
		)
		if err := rows.Scan(&r.Version, &r.Approved, &ts); err != nil {
			return nil, fmt.Errorf("store.SpecHistory scan: %w", err)
		}
		r.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", ts)
		out = append(out, r)
	}
	return out, rows.Err()
}

// ApproveSpec marks one version approved. Approval is per version, so editing
// a spec requires re-approval — without that the gate approves a spec once and
// then rubber-stamps everything after it.
func (s *Store) ApproveSpec(skill string, version int) error {
	res, err := s.db.Exec(`UPDATE specs SET approved = 1 WHERE skill_name = ? AND version = ?`, skill, version)
	if err != nil {
		return fmt.Errorf("store.ApproveSpec: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store.ApproveSpec rows: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("store.ApproveSpec: no version %d for %q", version, skill)
	}
	return nil
}
