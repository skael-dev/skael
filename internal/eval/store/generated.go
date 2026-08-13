package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// RecordGeneratedRef records the content ref of the eval set the generator
// last wrote for a skill. `suite push` compares the current ref against it: an
// equal ref means nobody read the file.
func (s *Store) RecordGeneratedRef(skill, ref string) error {
	_, err := s.db.Exec(`
		INSERT INTO suite_generated (skill_name, suite_ref) VALUES (?, ?)
		ON CONFLICT(skill_name) DO UPDATE SET suite_ref = excluded.suite_ref,
			written_at = datetime('now')`, skill, ref)
	if err != nil {
		return fmt.Errorf("store.RecordGeneratedRef %s: %w", skill, err)
	}
	return nil
}

// GeneratedRef returns the ref recorded by RecordGeneratedRef, or the empty
// string when there is none. An absent row is not an error. A suite
// written before this table existed has no record. The caller must treat
// that as possibly reviewed rather than as certainly unreviewed.
func (s *Store) GeneratedRef(skill string) (string, error) {
	var ref string
	err := s.db.QueryRow(`SELECT suite_ref FROM suite_generated WHERE skill_name = ?`, skill).Scan(&ref)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("store.GeneratedRef %s: %w", skill, err)
	}
	return ref, nil
}
