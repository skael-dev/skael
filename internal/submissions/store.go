// Package submissions holds skill archives that agents send for human review.
package submissions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/skael-dev/skael/internal/skill"
)

// The states of a submission.
const (
	StatePending  = "pending"
	StateApproved = "approved"
	StateRejected = "rejected"
)

// Submission is a skill archive awaiting or past a person's decision.
type Submission struct {
	ID            string            `json:"id"`
	SkillName     string            `json:"skill_name"`
	Consumer      string            `json:"consumer"`
	SubmittedBy   string            `json:"submitted_by"`
	Rationale     string            `json:"rationale"`
	RunLink       string            `json:"run_link"`
	ExternalRunID string            `json:"external_run_id"`
	ArchivePath   string            `json:"-"`
	Checksum      string            `json:"checksum"`
	FileManifest  []skill.FileEntry `json:"file_manifest"`
	ScanResult    json.RawMessage   `json:"scan_result"`
	State         string            `json:"state"`
	DecidedBy     string            `json:"decided_by"`
	DecidedAt     *time.Time        `json:"decided_at,omitempty"`
	Reason        string            `json:"reason"`
	Edited        bool              `json:"edited"`
	Version       *int              `json:"version,omitempty"`
	CreatedAt     time.Time         `json:"created_at"`
}

// Store persists submissions.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore constructs a Store.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

const columns = `id, skill_name, consumer, submitted_by, rationale, run_link, external_run_id,
	archive_path, checksum, file_manifest, scan_result, state, decided_by, decided_at, reason, edited, version, created_at`

func scanRow(row pgx.Row) (*Submission, error) {
	var sub Submission
	var manifest, scanResult []byte
	err := row.Scan(&sub.ID, &sub.SkillName, &sub.Consumer, &sub.SubmittedBy, &sub.Rationale, &sub.RunLink, &sub.ExternalRunID,
		&sub.ArchivePath, &sub.Checksum, &manifest, &scanResult, &sub.State, &sub.DecidedBy, &sub.DecidedAt, &sub.Reason, &sub.Edited, &sub.Version, &sub.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("submissions: scan: %w", err)
	}
	sub.ScanResult = scanResult
	if err := json.Unmarshal(manifest, &sub.FileManifest); err != nil {
		return nil, fmt.Errorf("submissions: manifest: %w", err)
	}
	return &sub, nil
}

// Create inserts a pending submission.
func (s *Store) Create(ctx context.Context, sub *Submission) (*Submission, error) {
	manifest, err := json.Marshal(sub.FileManifest)
	if err != nil {
		return nil, fmt.Errorf("submissions.Create: %w", err)
	}
	return scanRow(s.pool.QueryRow(ctx, `
		INSERT INTO submissions (skill_name, consumer, submitted_by, rationale, run_link, external_run_id, archive_path, checksum, file_manifest, scan_result)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING `+columns,
		sub.SkillName, sub.Consumer, sub.SubmittedBy, sub.Rationale, sub.RunLink, sub.ExternalRunID,
		sub.ArchivePath, sub.Checksum, manifest, []byte(sub.ScanResult)))
}

// Get returns a submission, or nil.
func (s *Store) Get(ctx context.Context, id string) (*Submission, error) {
	return scanRow(s.pool.QueryRow(ctx, `SELECT `+columns+` FROM submissions WHERE id = $1`, id))
}

// List returns submissions in a state, newest first. An empty state lists all.
func (s *Store) List(ctx context.Context, state string, limit int) ([]Submission, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+columns+` FROM submissions
		WHERE $1 = '' OR state = $1 ORDER BY created_at DESC LIMIT $2`, state, limit)
	if err != nil {
		return nil, fmt.Errorf("submissions.List: %w", err)
	}
	defer rows.Close()
	out := []Submission{}
	for rows.Next() {
		sub, err := scanRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sub)
	}
	return out, rows.Err()
}

// ErrDecided reports a submission that already has its decision.
var ErrDecided = errors.New("submission is already decided")

// Decide runs fn with the row locked, so two reviewers cannot both publish one
// submission. fn sees the transaction and sets the fields to store.
func (s *Store) Decide(ctx context.Context, id string, fn func(tx pgx.Tx, sub *Submission) error) (*Submission, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("submissions.Decide: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// NO KEY UPDATE, not UPDATE: a publish inside fn must not block on this row
	// through the foreign key that skill_versions.submission_id checks.
	sub, err := scanRow(tx.QueryRow(ctx, `SELECT `+columns+` FROM submissions WHERE id = $1 FOR NO KEY UPDATE`, id))
	if err != nil {
		return nil, err
	}
	if sub == nil {
		return nil, nil
	}
	if sub.State != StatePending {
		return nil, ErrDecided
	}
	if err := fn(tx, sub); err != nil {
		return nil, err
	}
	sub, err = scanRow(tx.QueryRow(ctx, `
		UPDATE submissions SET state = $2, decided_by = $3, decided_at = now(), reason = $4, edited = $5, version = $6
		WHERE id = $1 RETURNING `+columns, id, sub.State, sub.DecidedBy, sub.Reason, sub.Edited, sub.Version))
	if err != nil {
		return nil, err
	}
	return sub, tx.Commit(ctx)
}
