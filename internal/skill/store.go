package skill

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/skael-dev/skael/internal/gate"
)

// Store handles Postgres persistence for skills and their versions.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore constructs a Store backed by the given connection pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Create inserts a new skill row and returns the created record.
func (s *Store) Create(ctx context.Context, name, displayName, description, content string, frontmatter json.RawMessage) (*Skill, error) {
	const q = `
		INSERT INTO skills (name, display_name, description, content, frontmatter)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, name, display_name, description, content, latest_version, frontmatter,
		          author, license, compatibility, tags, spec_compliance,
		          created_at, updated_at, reviewed_at, reviewed_by
	`
	row := s.pool.QueryRow(ctx, q, name, displayName, description, content, frontmatter)
	sk, err := scanSkill(row)
	if err != nil {
		return nil, fmt.Errorf("skill.Store.Create: %w", err)
	}
	return sk, nil
}

// GetByName retrieves a skill by its unique name. Returns nil, nil when not found.
func (s *Store) GetByName(ctx context.Context, name string) (*Skill, error) {
	const q = `
		SELECT id, name, display_name, description, content, latest_version, frontmatter,
		       author, license, compatibility, tags, spec_compliance,
		       created_at, updated_at, reviewed_at, reviewed_by
		FROM skills
		WHERE name = $1
	`
	row := s.pool.QueryRow(ctx, q, name)
	sk, err := scanSkill(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("skill.Store.GetByName: %w", err)
	}
	return sk, nil
}

// ListOptions holds optional filter parameters for List.
type ListOptions struct {
	Limit   int
	Offset  int
	Author  string
	Tag     string
	License string
}

// List returns a paginated slice of skills along with the total row count.
// Filters are applied when the corresponding ListOptions fields are non-empty.
func (s *Store) List(ctx context.Context, opts ListOptions) ([]Skill, int, error) {
	if opts.Limit <= 0 {
		opts.Limit = 20
	}

	args := []any{}
	next := func(v any) string { args = append(args, v); return fmt.Sprintf("$%d", len(args)) }

	where := ""
	if opts.Author != "" {
		where += fmt.Sprintf(" AND author = %s", next(opts.Author))
	}
	if opts.Tag != "" {
		where += fmt.Sprintf(" AND %s = ANY(tags)", next(opts.Tag))
	}
	if opts.License != "" {
		where += fmt.Sprintf(" AND license = %s", next(opts.License))
	}

	countQ := `SELECT COUNT(*) FROM skills WHERE 1=1` + where
	var total int
	if err := s.pool.QueryRow(ctx, countQ, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("skill.Store.List count: %w", err)
	}

	q := `
		SELECT id, name, display_name, description, content, latest_version, frontmatter,
		       author, license, compatibility, tags, spec_compliance,
		       created_at, updated_at, reviewed_at, reviewed_by
		FROM skills
		WHERE 1=1` + where + `
		ORDER BY created_at DESC
		LIMIT ` + next(opts.Limit) + ` OFFSET ` + next(opts.Offset)

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("skill.Store.List query: %w", err)
	}
	defer rows.Close()

	var skills []Skill
	for rows.Next() {
		sk, err := scanSkill(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("skill.Store.List scan: %w", err)
		}
		skills = append(skills, *sk)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("skill.Store.List rows: %w", err)
	}
	return skills, total, nil
}

// Delete removes a skill (and its versions via CASCADE) by name.
func (s *Store) Delete(ctx context.Context, name string) error {
	const q = `DELETE FROM skills WHERE name = $1`
	if _, err := s.pool.Exec(ctx, q, name); err != nil {
		return fmt.Errorf("skill.Store.Delete: %w", err)
	}
	return nil
}

// CreateVersion inserts a new skill_versions row, updates the parent skill's
// description/content/frontmatter, and advances skills.latest_version to the
// new version only when the gate decision releases it — all within a single
// transaction. A held version exists and is stored, but is not served.
func (s *Store) CreateVersion(
	ctx context.Context,
	skillID, archivePath, checksum, changelog string,
	description, content string,
	frontmatter json.RawMessage,
	manifest []FileEntry,
	scanResult json.RawMessage,
	publishedBy string,
	decision gate.Decision,
) (*Version, error) {
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("skill.Store.CreateVersion marshal manifest: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("skill.Store.CreateVersion begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	state := "released"
	if decision.Outcome == gate.NeedsReview {
		state = "needs_review"
	}

	released := state == "released"

	// The UPDATE runs whatever the decision is, and not only for the columns
	// it writes: it takes the row lock that serialises two concurrent
	// publishes of the same skill. Without it, both would compute the same
	// MAX(version)+1 below and collide on UNIQUE(skill_id, version).
	// TestCreateVersionConcurrentPublishesDoNotCollide is what keeps this
	// honest — remove the statement and it goes red.
	//
	// The rendered body and metadata columns, though, are conditional: they
	// are what GET /api/skills/{name} serves. Writing them for a held version
	// would publish its prose beside a latest_version that still points at
	// the previous release — the gate withholding the archive while shipping
	// the text. The held version's own row keeps its frontmatter, so a review
	// screen loses nothing.
	const updateSkill = `
		UPDATE skills
		SET updated_at = now(),
		    reviewed_at = NULL, reviewed_by = '',
		    description = CASE WHEN $5 THEN $2 ELSE description END,
		    content     = CASE WHEN $5 THEN $3 ELSE content END,
		    frontmatter = CASE WHEN $5 THEN $4 ELSE frontmatter END
		WHERE id = $1
	`
	if _, err := tx.Exec(ctx, updateSkill, skillID, description, content, frontmatter, released); err != nil {
		return nil, fmt.Errorf("skill.Store.CreateVersion update skill: %w", err)
	}

	// The version number is the sequence of versions that exist, which is no
	// longer the same thing as the pointer to the newest servable one.
	const nextVersion = `
		SELECT COALESCE(MAX(version), 0) + 1 FROM skill_versions WHERE skill_id = $1
	`
	var newVersion int
	if err := tx.QueryRow(ctx, nextVersion, skillID).Scan(&newVersion); err != nil {
		return nil, fmt.Errorf("skill.Store.CreateVersion next version: %w", err)
	}

	decisionJSON, err := json.Marshal(decision)
	if err != nil {
		return nil, fmt.Errorf("skill.Store.CreateVersion marshal decision: %w", err)
	}

	// Insert the version row.
	const insertVersion = `
		INSERT INTO skill_versions
			(skill_id, version, archive_path, checksum, changelog, frontmatter,
			 file_manifest, scan_result, published_by, gate_state, gate_decision,
			 description, content)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		RETURNING id, skill_id, version, archive_path, checksum, changelog, frontmatter,
		          file_manifest, scan_result, published_by, created_at,
		          gate_state, gate_decision, gated_by, gated_at, gate_note,
		          description, content
	`
	row := tx.QueryRow(ctx, insertVersion,
		skillID, newVersion, archivePath, checksum, changelog,
		frontmatter, manifestJSON, scanResult, publishedBy, state, decisionJSON,
		description, content,
	)
	ver, err := scanVersion(row)
	if err != nil {
		return nil, fmt.Errorf("skill.Store.CreateVersion insert: %w", err)
	}

	// latest_version advances only for a released version.
	//
	// GREATEST is defensive here and cannot be exercised from this function:
	// newVersion is MAX(version)+1, so it is always the new maximum and always
	// greater than latest_version. The case it guards — a held v4 released
	// after a clean v5 already shipped, where a bare assignment would regress
	// every client to the older skill — arises on the release path, not on
	// publish. The live behaviour belongs to ReleaseVersion.
	if released {
		const advance = `UPDATE skills SET latest_version = GREATEST(latest_version, $2) WHERE id = $1`
		if _, err := tx.Exec(ctx, advance, skillID, newVersion); err != nil {
			return nil, fmt.Errorf("skill.Store.CreateVersion advance latest: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("skill.Store.CreateVersion commit: %w", err)
	}
	return ver, nil
}

// ListVersions returns all versions for a skill ordered by version DESC.
func (s *Store) ListVersions(ctx context.Context, skillName string) ([]Version, error) {
	const q = `
		SELECT sv.id, sv.skill_id, sv.version, sv.archive_path, sv.checksum,
		       sv.changelog, sv.frontmatter, sv.file_manifest, sv.scan_result,
		       sv.published_by, sv.created_at,
		       sv.gate_state, sv.gate_decision, sv.gated_by, sv.gated_at, sv.gate_note,
		       sv.description, sv.content
		FROM skill_versions sv
		JOIN skills sk ON sk.id = sv.skill_id
		WHERE sk.name = $1
		ORDER BY sv.version DESC
	`
	rows, err := s.pool.Query(ctx, q, skillName)
	if err != nil {
		return nil, fmt.Errorf("skill.Store.ListVersions query: %w", err)
	}
	defer rows.Close()

	var versions []Version
	for rows.Next() {
		ver, err := scanVersion(rows)
		if err != nil {
			return nil, fmt.Errorf("skill.Store.ListVersions scan: %w", err)
		}
		versions = append(versions, *ver)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("skill.Store.ListVersions rows: %w", err)
	}
	return versions, nil
}

// GetVersion retrieves a specific version of a skill. Returns nil, nil if not found.
func (s *Store) GetVersion(ctx context.Context, skillName string, version int) (*Version, error) {
	const q = `
		SELECT sv.id, sv.skill_id, sv.version, sv.archive_path, sv.checksum,
		       sv.changelog, sv.frontmatter, sv.file_manifest, sv.scan_result,
		       sv.published_by, sv.created_at,
		       sv.gate_state, sv.gate_decision, sv.gated_by, sv.gated_at, sv.gate_note,
		       sv.description, sv.content
		FROM skill_versions sv
		JOIN skills sk ON sk.id = sv.skill_id
		WHERE sk.name = $1 AND sv.version = $2
	`
	row := s.pool.QueryRow(ctx, q, skillName, version)
	ver, err := scanVersion(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("skill.Store.GetVersion: %w", err)
	}
	return ver, nil
}

// scanner is satisfied by both *pgx.Row and pgx.Rows.
type scanner interface {
	Scan(dest ...any) error
}

func scanSkill(row scanner) (*Skill, error) {
	var sk Skill
	var rawFrontmatter []byte
	err := row.Scan(
		&sk.ID,
		&sk.Name,
		&sk.DisplayName,
		&sk.Description,
		&sk.Content,
		&sk.LatestVersion,
		&rawFrontmatter,
		&sk.Author,
		&sk.License,
		&sk.Compatibility,
		&sk.Tags,
		&sk.SpecCompliance,
		&sk.CreatedAt,
		&sk.UpdatedAt,
		&sk.ReviewedAt,
		&sk.ReviewedBy,
	)
	if err != nil {
		return nil, err
	}
	sk.Frontmatter = json.RawMessage(rawFrontmatter)
	if sk.Tags == nil {
		sk.Tags = []string{}
	}
	return &sk, nil
}

// SetReview marks a skill as reviewed by the given reviewer.
func (s *Store) SetReview(ctx context.Context, name, reviewedBy string) error {
	const q = `UPDATE skills SET reviewed_at = now(), reviewed_by = $2 WHERE name = $1`
	tag, err := s.pool.Exec(ctx, q, name, reviewedBy)
	if err != nil {
		return fmt.Errorf("skill.Store.SetReview: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("skill.Store.SetReview: skill %q not found", name)
	}
	return nil
}

// ClearReview removes the review mark from a skill.
func (s *Store) ClearReview(ctx context.Context, name string) error {
	const q = `UPDATE skills SET reviewed_at = NULL, reviewed_by = '' WHERE name = $1`
	tag, err := s.pool.Exec(ctx, q, name)
	if err != nil {
		return fmt.Errorf("skill.Store.ClearReview: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("skill.Store.ClearReview: skill %q not found", name)
	}
	return nil
}

// BulkSetReview marks multiple skills as reviewed in a single UPDATE.
// Returns the number of rows actually updated.
func (s *Store) BulkSetReview(ctx context.Context, names []string, reviewedBy string) (int, error) {
	const q = `UPDATE skills SET reviewed_at = now(), reviewed_by = $2 WHERE name = ANY($1)`
	tag, err := s.pool.Exec(ctx, q, names, reviewedBy)
	if err != nil {
		return 0, fmt.Errorf("skill.Store.BulkSetReview: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// UpdateSpecFields updates the spec-derived metadata columns on a skill row.
func (s *Store) UpdateSpecFields(ctx context.Context, name, author, license, compat, compliance, displayName string, tags []string) error {
	return updateSpecFieldsExec(ctx, s.pool, name, author, license, compat, compliance, displayName, tags)
}

// updateSpecFieldsExec is UpdateSpecFields against an arbitrary executor, so a
// release running inside the caller's transaction writes these columns in the
// same transaction that advanced the pointer.
func updateSpecFieldsExec(ctx context.Context, e Executor, name, author, license, compat, compliance, displayName string, tags []string) error {
	const q = `
		UPDATE skills
		SET author = $2, license = $3, compatibility = $4,
		    spec_compliance = $5, display_name = $6, tags = $7
		WHERE name = $1
	`
	tag, err := e.Exec(ctx, q, name, author, license, compat, compliance, displayName, tags)
	if err != nil {
		return fmt.Errorf("skill.Store.UpdateSpecFields: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("skill.Store.UpdateSpecFields: skill %q not found", name)
	}
	return nil
}

func scanVersion(row scanner) (*Version, error) {
	var ver Version
	var rawFrontmatter, rawFileManifest, rawScanResult, rawGateDecision []byte
	err := row.Scan(
		&ver.ID,
		&ver.SkillID,
		&ver.Version,
		&ver.ArchivePath,
		&ver.Checksum,
		&ver.Changelog,
		&rawFrontmatter,
		&rawFileManifest,
		&rawScanResult,
		&ver.PublishedBy,
		&ver.CreatedAt,
		&ver.GateState,
		&rawGateDecision,
		&ver.GatedBy,
		&ver.GatedAt,
		&ver.GateNote,
		&ver.Description,
		&ver.Content,
	)
	if err != nil {
		return nil, err
	}
	ver.Frontmatter = json.RawMessage(rawFrontmatter)
	ver.ScanResult = json.RawMessage(rawScanResult)
	ver.GateDecision = json.RawMessage(rawGateDecision)
	if err := json.Unmarshal(rawFileManifest, &ver.FileManifest); err != nil {
		return nil, fmt.Errorf("unmarshal file_manifest: %w", err)
	}
	return &ver, nil
}
