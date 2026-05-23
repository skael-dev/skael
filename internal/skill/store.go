package skill

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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
		RETURNING id, name, display_name, description, content, latest_version, frontmatter, created_at, updated_at
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
		SELECT id, name, display_name, description, content, latest_version, frontmatter, created_at, updated_at
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

// List returns a paginated slice of skills along with the total row count.
func (s *Store) List(ctx context.Context, limit, offset int) ([]Skill, int, error) {
	const countQ = `SELECT COUNT(*) FROM skills`
	var total int
	if err := s.pool.QueryRow(ctx, countQ).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("skill.Store.List count: %w", err)
	}

	const q = `
		SELECT id, name, display_name, description, content, latest_version, frontmatter, created_at, updated_at
		FROM skills
		ORDER BY created_at DESC
		LIMIT $1 OFFSET $2
	`
	rows, err := s.pool.Query(ctx, q, limit, offset)
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

// UpdateContent updates a skill's description, content, and frontmatter, bumping updated_at.
func (s *Store) UpdateContent(ctx context.Context, name, description, content string, frontmatter json.RawMessage) error {
	const q = `
		UPDATE skills
		SET description = $2, content = $3, frontmatter = $4, updated_at = now()
		WHERE name = $1
	`
	if _, err := s.pool.Exec(ctx, q, name, description, content, frontmatter); err != nil {
		return fmt.Errorf("skill.Store.UpdateContent: %w", err)
	}
	return nil
}

// CreateVersion increments latest_version on the parent skill and inserts a
// new skill_versions row, all within a single transaction.
func (s *Store) CreateVersion(
	ctx context.Context,
	skillID, archivePath, checksum, changelog string,
	frontmatter json.RawMessage,
	manifest []FileEntry,
	scanResult json.RawMessage,
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

	// Increment latest_version and return the new value.
	const updateSkill = `
		UPDATE skills
		SET latest_version = latest_version + 1, updated_at = now()
		WHERE id = $1
		RETURNING latest_version
	`
	var newVersion int
	if err := tx.QueryRow(ctx, updateSkill, skillID).Scan(&newVersion); err != nil {
		return nil, fmt.Errorf("skill.Store.CreateVersion update skill: %w", err)
	}

	// Insert the version row.
	const insertVersion = `
		INSERT INTO skill_versions (skill_id, version, archive_path, checksum, changelog, frontmatter, file_manifest, scan_result)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, skill_id, version, archive_path, checksum, changelog, frontmatter, file_manifest, scan_result, published_by, created_at
	`
	row := tx.QueryRow(ctx, insertVersion,
		skillID, newVersion, archivePath, checksum, changelog,
		frontmatter, manifestJSON, scanResult,
	)
	ver, err := scanVersion(row)
	if err != nil {
		return nil, fmt.Errorf("skill.Store.CreateVersion insert: %w", err)
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
		       sv.published_by, sv.created_at
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
		       sv.published_by, sv.created_at
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
		&sk.CreatedAt,
		&sk.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	sk.Frontmatter = json.RawMessage(rawFrontmatter)
	return &sk, nil
}

func scanVersion(row scanner) (*Version, error) {
	var ver Version
	var rawFrontmatter, rawFileManifest, rawScanResult []byte
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
	)
	if err != nil {
		return nil, err
	}
	ver.Frontmatter = json.RawMessage(rawFrontmatter)
	ver.ScanResult = json.RawMessage(rawScanResult)
	if err := json.Unmarshal(rawFileManifest, &ver.FileManifest); err != nil {
		return nil, fmt.Errorf("unmarshal file_manifest: %w", err)
	}
	return &ver, nil
}
