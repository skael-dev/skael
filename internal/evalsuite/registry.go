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
	"github.com/jackc/pgx/v5/pgconn"
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
	// Spec is the authored spec.SkillSpec, as JSON, that this suite was
	// checked against. A published bundle never carries spec.yaml — it is
	// authoring scaffolding, stripped before packing — so this is the only
	// channel a worker rebuilding a workspace from a downloaded bundle has to
	// recover it. Nil when the caller that pushed this suite predates this
	// field, or genuinely has no spec to send.
	Spec       json.RawMessage
	Origin     Origin
	UploadedBy string
	CreatedAt  time.Time
}

// Origin records how a suite came to exist.
type Origin string

const (
	// OriginAuthored is a suite a person wrote and gated through whetstone.
	OriginAuthored Origin = "authored"
	// OriginDerived is a suite generated from the skill's own SKILL.md. A
	// score against one measures the skill against its own claims, which is
	// why internal/skill's Releaser will not let it clear a scan hold.
	OriginDerived Origin = "derived"
)

// Queryer is the subset of pgx both a pool and a transaction satisfy, so
// MarkDerived can be composed into the report handler's transaction rather
// than landing outside it and surviving a rolled-back score.
type Queryer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// ErrInvalidArchive is wrapped into any Put error caused by the caller's
// archive itself (unpack failure, unreadable suite tree, missing checks) —
// bad input that should be reported to the caller as a 4xx, as opposed to a
// storage or database failure on the server's side.
var ErrInvalidArchive = errors.New("evalsuite: invalid archive")

// ErrNotFound is wrapped into any Get/LatestForSkill error caused by no
// matching row existing, as opposed to a database failure while looking one
// up.
var ErrNotFound = errors.New("evalsuite: suite not found")

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
// construction rather than by convention. specJSON is the pusher's spec.yaml
// as JSON (may be nil/empty — see Record.Spec).
func (r *Registry) Put(ctx context.Context, skillName string, archive []byte, checks []Check, specVersion int, uploadedBy string, specJSON json.RawMessage) (*Record, error) {
	return r.put(ctx, skillName, archive, checks, specVersion, uploadedBy, specJSON, OriginAuthored, nil)
}

// PutDerived stores a suite the server has itself established is machine
// derived, marking it so in the same transaction as the insert and running
// after (when non-nil) inside it too — that is how the job row that caused
// the derivation gets its suite_ref without a second, separately-failable
// write. Origin is never taken from the pusher: a worker that could declare
// its own suite authored would defeat internal/skill's refusal to let a
// derived score clear a scan hold.
func (r *Registry) PutDerived(ctx context.Context, skillName string, archive []byte, checks []Check, specVersion int, uploadedBy string, specJSON json.RawMessage, after func(ctx context.Context, q Queryer, ref string) error) (*Record, error) {
	return r.put(ctx, skillName, archive, checks, specVersion, uploadedBy, specJSON, OriginDerived, after)
}

func (r *Registry) put(ctx context.Context, skillName string, archive []byte, checks []Check, specVersion int, uploadedBy string, specJSON json.RawMessage, origin Origin, after func(ctx context.Context, q Queryer, ref string) error) (*Record, error) {
	if len(checks) == 0 {
		return nil, fmt.Errorf("evalsuite: Put requires at least one suite check result, got none: %w", ErrInvalidArchive)
	}

	dir, err := os.MkdirTemp("", "evalsuite-put-*")
	if err != nil {
		return nil, fmt.Errorf("evalsuite: Put temp dir: %w", err)
	}
	defer os.RemoveAll(dir)

	if err := Unpack(archive, dir); err != nil {
		return nil, fmt.Errorf("evalsuite: Put unpack: %w: %w", ErrInvalidArchive, err)
	}

	ref, err := suite.Ref(dir)
	if err != nil {
		return nil, fmt.Errorf("evalsuite: Put ref: %w: %w", ErrInvalidArchive, err)
	}

	set, err := suite.LoadEvalSet(dir)
	if err != nil {
		return nil, fmt.Errorf("evalsuite: Put load: %w: %w", ErrInvalidArchive, err)
	}
	taskCount := len(set.Evals)

	archivePath := archiveKey(ref)
	if _, err := r.st.Write(ctx, archivePath, bytes.NewReader(archive)); err != nil {
		return nil, fmt.Errorf("evalsuite: Put storage write: %w", err)
	}

	checksJSON, err := json.Marshal(checks)
	if err != nil {
		return nil, fmt.Errorf("evalsuite: Put marshal checks: %w", err)
	}

	// A caller that marshals a nil Go value still produces the 4-byte JSON
	// literal "null" rather than an empty byte slice, and storing that in the
	// jsonb column round-trips as "spec": null on read — which fools
	// unmarshalSuiteSpec into thinking a spec was provided. Normalize both
	// "empty" and "null" to a real SQL NULL here so the column never holds
	// the literal.
	var specParam any
	if len(specJSON) > 0 && string(bytes.TrimSpace(specJSON)) != "null" {
		specParam = specJSON
	} // else left nil -> NULL

	const insert = `
		INSERT INTO eval_suites (ref, skill_name, archive_path, task_count, checks, spec_version, uploaded_by, spec)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (ref) DO NOTHING
	`
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("evalsuite: Put begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op once committed

	if _, err := tx.Exec(ctx, insert, ref, skillName, archivePath, taskCount, checksJSON, specVersion, uploadedBy, specParam); err != nil {
		return nil, fmt.Errorf("evalsuite: Put insert: %w", err)
	}
	// MarkDerived rather than an origin column in the insert: it is an
	// upgrade-only update, so re-pushing content already recorded as derived
	// (the insert is ON CONFLICT DO NOTHING) still ends derived, and an
	// authored push can never walk a derived suite back.
	if origin == OriginDerived {
		if err := r.MarkDerived(ctx, tx, ref); err != nil {
			return nil, err
		}
	}
	if after != nil {
		if err := after(ctx, tx, ref); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("evalsuite: Put commit: %w", err)
	}

	return r.Get(ctx, ref)
}

// Get returns the stored record for ref.
func (r *Registry) Get(ctx context.Context, ref string) (*Record, error) {
	const q = `
		SELECT ref, skill_name, archive_path, task_count, checks, spec_version, uploaded_by, created_at, spec, origin
		FROM eval_suites
		WHERE ref = $1
	`
	row := r.db.QueryRow(ctx, q, ref)
	rec, err := scanRecord(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("evalsuite: no suite recorded for ref %s: %w", ref, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("evalsuite: Get: %w", err)
	}
	return rec, nil
}

// ReadArchive returns a stored suite's archive bytes. A route uses it to
// look inside a suite. The route does not reach through the registry into
// storage itself.
func (r *Registry) ReadArchive(ctx context.Context, ref string) ([]byte, error) {
	rec, err := r.Get(ctx, ref)
	if err != nil {
		return nil, err
	}
	rc, err := r.st.Read(ctx, rec.ArchivePath)
	if err != nil {
		return nil, fmt.Errorf("evalsuite: ReadArchive %s: %w", ref, err)
	}
	defer func() { _ = rc.Close() }()
	return io.ReadAll(rc)
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
		SELECT ref, skill_name, archive_path, task_count, checks, spec_version, uploaded_by, created_at, spec, origin
		FROM eval_suites
		WHERE skill_name = $1
		ORDER BY created_at DESC
		LIMIT 1
	`
	row := r.db.QueryRow(ctx, q, skillName)
	rec, err := scanRecord(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("evalsuite: no suite recorded for skill %s: %w", skillName, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("evalsuite: LatestForSkill: %w", err)
	}
	return rec, nil
}

// MarkDerived flags ref as machine-derived. It takes a Queryer so the caller
// can write it inside the same transaction as the score that justifies it.
func (r *Registry) MarkDerived(ctx context.Context, q Queryer, ref string) error {
	tag, err := q.Exec(ctx, `UPDATE eval_suites SET origin = $1 WHERE ref = $2`, string(OriginDerived), ref)
	if err != nil {
		return fmt.Errorf("evalsuite: MarkDerived %s: %w", ref, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("evalsuite: MarkDerived: no suite recorded for ref %s: %w", ref, ErrNotFound)
	}
	return nil
}

// MarkAuthored flags ref as reviewed by a person. It takes a Queryer so the
// caller can write it inside the same transaction as the review that
// justifies it.
//
// This is the one path that can raise a suite's origin. Its only caller
// runs behind an authenticated user who acts in the review view. No client
// declaration reaches it: a pusher that claims authored clears its own scan
// hold.
func (r *Registry) MarkAuthored(ctx context.Context, q Queryer, ref string) error {
	tag, err := q.Exec(ctx, `UPDATE eval_suites SET origin = $1 WHERE ref = $2`, string(OriginAuthored), ref)
	if err != nil {
		return fmt.Errorf("evalsuite: MarkAuthored %s: %w", ref, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("evalsuite: MarkAuthored: no suite recorded for ref %s: %w", ref, ErrNotFound)
	}
	return nil
}

func scanRecord(row pgx.Row) (*Record, error) {
	var rec Record
	var checksJSON []byte
	var specJSON []byte
	if err := row.Scan(&rec.Ref, &rec.SkillName, &rec.ArchivePath, &rec.TaskCount, &checksJSON, &rec.SpecVersion, &rec.UploadedBy, &rec.CreatedAt, &specJSON, &rec.Origin); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(checksJSON, &rec.Checks); err != nil {
		return nil, fmt.Errorf("evalsuite: unmarshal checks: %w", err)
	}
	if len(specJSON) > 0 {
		rec.Spec = specJSON
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
