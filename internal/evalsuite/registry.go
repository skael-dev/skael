// Package evalsuite is the registry-side counterpart to internal/eval/suite:
// evaluation task-suites become content-addressable artifacts stored
// alongside skill bundles, so a quality score can be re-run later against the
// exact same tasks with a different model panel. Regenerating a suite would
// silently change what the score measures, which is why suites are stored
// rather than recomputed.
package evalsuite

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/skael-dev/skael/internal/eval/suite"
	"github.com/skael-dev/skael/internal/platform"
	skillpkg "github.com/skael-dev/skael/internal/skill"
)

// Check is one oracle-gate result the author recorded for a suite's tasks: a
// deterministic run of the task's own oracle solution against its own
// verifier, proving the task is solvable and the verifier is not broken.
type Check struct {
	TaskID string `json:"task_id"`
	OK     bool   `json:"ok"`
	Void   bool   `json:"void"`
	Reason string `json:"reason,omitempty"`
}

// Record is a stored suite: its content-addressed ref, the archive location,
// and the oracle-gate checks that travel with it.
type Record struct {
	Ref         string
	SkillName   string
	ArchivePath string
	TaskCount   int
	Checks      []Check
	SpecVersion int
	UploadedBy  string
	CreatedAt   time.Time
}

// Registry stores suite archives content-addressably and records their
// metadata in eval_suites.
type Registry struct {
	db *pgxpool.Pool
	st platform.Storage
}

// NewRegistry constructs a Registry backed by db and st.
func NewRegistry(db *pgxpool.Pool, st platform.Storage) *Registry {
	return &Registry{db: db, st: st}
}

// archiveKey is the storage key for a suite's archive, given its ref.
func archiveKey(ref string) string {
	return fmt.Sprintf("suites/%s.tar.gz", ref)
}

// Put stores a suite archive under suites/{ref}.tar.gz and records it. It is
// idempotent on ref: the same content uploaded twice is one row.
//
// checks must not be empty — a suite with no oracle-gate results cannot tell
// a broken task from a broken skill, so the check travels with the suite by
// construction rather than by convention.
func (r *Registry) Put(ctx context.Context, skillName string, archive []byte, checks []Check, specVersion int, uploadedBy string) (*Record, error) {
	if len(checks) == 0 {
		return nil, fmt.Errorf("evalsuite: Put requires at least one suite check result, got none")
	}

	dir, err := os.MkdirTemp("", "evalsuite-put-*")
	if err != nil {
		return nil, fmt.Errorf("evalsuite: Put temp dir: %w", err)
	}
	defer os.RemoveAll(dir)

	if err := Unpack(archive, dir); err != nil {
		return nil, fmt.Errorf("evalsuite: Put unpack: %w", err)
	}

	ref, err := suite.Ref(dir)
	if err != nil {
		return nil, fmt.Errorf("evalsuite: Put ref: %w", err)
	}

	s, err := suite.Load(dir)
	if err != nil {
		return nil, fmt.Errorf("evalsuite: Put load: %w", err)
	}
	taskCount := len(s.Tasks)

	archivePath := archiveKey(ref)
	if _, err := r.st.Write(ctx, archivePath, bytes.NewReader(archive)); err != nil {
		return nil, fmt.Errorf("evalsuite: Put storage write: %w", err)
	}

	checksJSON, err := json.Marshal(checks)
	if err != nil {
		return nil, fmt.Errorf("evalsuite: Put marshal checks: %w", err)
	}

	const insert = `
		INSERT INTO eval_suites (ref, skill_name, archive_path, task_count, checks, spec_version, uploaded_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (ref) DO NOTHING
	`
	if _, err := r.db.Exec(ctx, insert, ref, skillName, archivePath, taskCount, checksJSON, specVersion, uploadedBy); err != nil {
		return nil, fmt.Errorf("evalsuite: Put insert: %w", err)
	}

	return r.Get(ctx, ref)
}

// Get returns the stored record for ref.
func (r *Registry) Get(ctx context.Context, ref string) (*Record, error) {
	const q = `
		SELECT ref, skill_name, archive_path, task_count, checks, spec_version, uploaded_by, created_at
		FROM eval_suites
		WHERE ref = $1
	`
	row := r.db.QueryRow(ctx, q, ref)
	rec, err := scanRecord(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("evalsuite: no suite recorded for ref %s", ref)
	}
	if err != nil {
		return nil, fmt.Errorf("evalsuite: Get: %w", err)
	}
	return rec, nil
}

// Fetch returns the raw archive bytes for ref.
func (r *Registry) Fetch(ctx context.Context, ref string) ([]byte, error) {
	rec, err := r.Get(ctx, ref)
	if err != nil {
		return nil, err
	}
	rc, err := r.st.Read(ctx, rec.ArchivePath)
	if err != nil {
		return nil, fmt.Errorf("evalsuite: Fetch storage read: %w", err)
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("evalsuite: Fetch read: %w", err)
	}
	return b, nil
}

// LatestForSkill returns the most recently created suite recorded for
// skillName.
func (r *Registry) LatestForSkill(ctx context.Context, skillName string) (*Record, error) {
	const q = `
		SELECT ref, skill_name, archive_path, task_count, checks, spec_version, uploaded_by, created_at
		FROM eval_suites
		WHERE skill_name = $1
		ORDER BY created_at DESC
		LIMIT 1
	`
	row := r.db.QueryRow(ctx, q, skillName)
	rec, err := scanRecord(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("evalsuite: no suite recorded for skill %s", skillName)
	}
	if err != nil {
		return nil, fmt.Errorf("evalsuite: LatestForSkill: %w", err)
	}
	return rec, nil
}

func scanRecord(row pgx.Row) (*Record, error) {
	var rec Record
	var checksJSON []byte
	if err := row.Scan(&rec.Ref, &rec.SkillName, &rec.ArchivePath, &rec.TaskCount, &checksJSON, &rec.SpecVersion, &rec.UploadedBy, &rec.CreatedAt); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(checksJSON, &rec.Checks); err != nil {
		return nil, fmt.Errorf("evalsuite: unmarshal checks: %w", err)
	}
	return &rec, nil
}

// Unpack extracts a suite archive into dir under the same limits skill.Unpack
// applies: no symlinks, no hardlinks, no entry over 1MiB, 50MB total. It
// reuses skill.Unpack directly rather than reimplementing the limits, so the
// two extraction paths cannot drift apart.
func Unpack(archive []byte, dir string) error {
	return skillpkg.Unpack(bytes.NewReader(archive), dir)
}

// PackDir builds a suite archive from a whetstone suite directory. Unlike
// skill.Pack, it does not require a SKILL.md — a suite directory has no such
// file — so it cannot simply call skill.Pack; the tar/gzip construction below
// mirrors it.
func PackDir(dir string) ([]byte, error) {
	var files []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("evalsuite: PackDir walk: %w", err)
	}

	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)

	for _, fullPath := range files {
		rel, err := filepath.Rel(dir, fullPath)
		if err != nil {
			return nil, fmt.Errorf("evalsuite: PackDir rel: %w", err)
		}
		rel = filepath.ToSlash(rel)

		info, err := os.Stat(fullPath)
		if err != nil {
			return nil, fmt.Errorf("evalsuite: PackDir stat %s: %w", rel, err)
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return nil, fmt.Errorf("evalsuite: PackDir header %s: %w", rel, err)
		}
		hdr.Name = rel

		if err := tw.WriteHeader(hdr); err != nil {
			return nil, fmt.Errorf("evalsuite: PackDir write header %s: %w", rel, err)
		}
		f, err := os.Open(fullPath)
		if err != nil {
			return nil, fmt.Errorf("evalsuite: PackDir open %s: %w", rel, err)
		}
		_, err = io.Copy(tw, f)
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("evalsuite: PackDir copy %s: %w", rel, err)
		}
	}

	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("evalsuite: PackDir close tar: %w", err)
	}
	if err := gzw.Close(); err != nil {
		return nil, fmt.Errorf("evalsuite: PackDir close gzip: %w", err)
	}
	return buf.Bytes(), nil
}
