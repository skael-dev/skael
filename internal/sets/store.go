// Package sets holds named lists of skills and their immutable resolutions.
package sets

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/skael-dev/skael/internal/audit"
	"github.com/skael-dev/skael/internal/scan"
)

// Querier runs against a pool or a transaction.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// SetEntry is one skill in a set. Version 0 means latest.
type SetEntry struct {
	Name    string `json:"name"`
	Version int    `json:"version,omitempty" doc:"An exact version. Omit it for latest."`
}

// Set is a named list of skills.
type Set struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Entries     []SetEntry `json:"entries"`
	CreatedBy   string     `json:"created_by"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// SetCaller is the last use of a set by one identity.
type SetCaller struct {
	Name         string    `json:"name"`
	ResolutionID string    `json:"resolution_id"`
	LastSeenAt   time.Time `json:"last_seen_at"`
}

// ResolvedEntry is one exact version in a resolution.
type ResolvedEntry struct {
	Name          string `json:"name"`
	Version       int    `json:"version"`
	SHA256        string `json:"sha256"`
	ScanVerdict   string `json:"scan_verdict"`
	ApprovalState string `json:"approval_state"`
	// RemovedFromGit warns that the skill's folder left its git repo. The
	// last version stays served until someone deletes the skill.
	RemovedFromGit bool `json:"removed_from_git,omitempty"`
}

// ExcludedEntry is a set entry that a resolve could not serve.
type ExcludedEntry struct {
	Name      string `json:"name"`
	Requested string `json:"requested"`
	Reason    string `json:"reason"`
}

// The reasons an entry is excluded.
const (
	ReasonSkillNotFound     = "skill_not_found"
	ReasonNoReleasedVersion = "no_released_version"
	ReasonVersionNotFound   = "version_not_found"
	ReasonHeld              = "held"
	ReasonRejected          = "rejected"
)

// Resolution is the immutable result of resolving a set.
type Resolution struct {
	ID       string          `json:"id"`
	Set      string          `json:"set"`
	Complete bool            `json:"complete"`
	Entries  []ResolvedEntry `json:"entries"`
	Excluded []ExcludedEntry `json:"excluded,omitempty"`
}

// ResolutionID hashes the sorted name, version and digest list.
func ResolutionID(entries []ResolvedEntry) string {
	lines := make([]string, len(entries))
	for i, e := range entries {
		lines[i] = fmt.Sprintf("%s\x00%d\x00%s\n", e.Name, e.Version, e.SHA256)
	}
	slices.Sort(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "")))
	return hex.EncodeToString(sum[:])
}

// ChangeNotifier hears of a set edit inside its transaction.
type ChangeNotifier interface {
	SetChanged(ctx context.Context, tx pgx.Tx, setID string) error
}

// Store persists sets.
type Store struct {
	pool     *pgxpool.Pool
	notifier ChangeNotifier
}

// SetNotifier attaches the receiver of set edits.
func (s *Store) SetNotifier(n ChangeNotifier) { s.notifier = n }

// NewStore constructs a Store.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// ErrNameTaken reports a duplicate set name.
var ErrNameTaken = errors.New("set name already exists")

func (s *Store) loadEntries(ctx context.Context, q Querier, set *Set) error {
	rows, err := q.Query(ctx, `SELECT skill_name, version FROM set_entries WHERE set_id = $1 ORDER BY skill_name`, set.ID)
	if err != nil {
		return fmt.Errorf("sets: load entries: %w", err)
	}
	set.Entries, err = pgx.CollectRows(rows, pgx.RowToStructByPos[SetEntry])
	if err != nil {
		return fmt.Errorf("sets: load entries: %w", err)
	}
	if set.Entries == nil {
		set.Entries = []SetEntry{}
	}
	return nil
}

const setColumns = `id, name, description, created_by, created_at, updated_at`

func scanSet(row pgx.Row) (*Set, error) {
	var set Set
	err := row.Scan(&set.ID, &set.Name, &set.Description, &set.CreatedBy, &set.CreatedAt, &set.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sets: scan: %w", err)
	}
	return &set, nil
}

// Get returns a set with its entries, or nil.
func (s *Store) Get(ctx context.Context, name string) (*Set, error) {
	set, err := scanSet(s.pool.QueryRow(ctx, `SELECT `+setColumns+` FROM sets WHERE name = $1`, name))
	if err != nil || set == nil {
		return nil, err
	}
	return set, s.loadEntries(ctx, s.pool, set)
}

// GetByID returns a set with its entries, or nil.
func (s *Store) GetByID(ctx context.Context, id string) (*Set, error) {
	set, err := scanSet(s.pool.QueryRow(ctx, `SELECT `+setColumns+` FROM sets WHERE id = $1`, id))
	if err != nil || set == nil {
		return nil, err
	}
	return set, s.loadEntries(ctx, s.pool, set)
}

// List returns every set with its entries.
func (s *Store) List(ctx context.Context) ([]Set, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+setColumns+` FROM sets ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("sets.List: %w", err)
	}
	defer rows.Close()
	out := []Set{}
	for rows.Next() {
		var set Set
		if err := rows.Scan(&set.ID, &set.Name, &set.Description, &set.CreatedBy, &set.CreatedAt, &set.UpdatedAt); err != nil {
			return nil, fmt.Errorf("sets.List: %w", err)
		}
		out = append(out, set)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sets.List: %w", err)
	}
	for i := range out {
		if err := s.loadEntries(ctx, s.pool, &out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func writeEntries(ctx context.Context, q Querier, setID string, entries []SetEntry) error {
	if _, err := q.Exec(ctx, `DELETE FROM set_entries WHERE set_id = $1`, setID); err != nil {
		return fmt.Errorf("sets: clear entries: %w", err)
	}
	for _, e := range entries {
		if _, err := q.Exec(ctx, `INSERT INTO set_entries (set_id, skill_name, version) VALUES ($1, $2, $3)`, setID, e.Name, e.Version); err != nil {
			return fmt.Errorf("sets: write entry: %w", err)
		}
	}
	return nil
}

// Create inserts a set and records it in the audit log.
func (s *Store) Create(ctx context.Context, name, description string, entries []SetEntry, actor string) (*Set, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("sets.Create: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	set, err := scanSet(tx.QueryRow(ctx, `
		INSERT INTO sets (name, description, created_by) VALUES ($1, $2, $3)
		RETURNING `+setColumns, name, description, actor))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, ErrNameTaken
		}
		return nil, fmt.Errorf("sets.Create: %w", err)
	}
	if err := writeEntries(ctx, tx, set.ID, entries); err != nil {
		return nil, err
	}
	set.Entries = entries
	if err := audit.Record(ctx, tx, actor, "set.create", "set:"+name, map[string]any{"entries": entries}); err != nil {
		return nil, err
	}
	// Records the first id, so the first real change has one to differ from.
	if _, _, _, err := s.Recheck(ctx, tx, set.ID); err != nil {
		return nil, err
	}
	return set, tx.Commit(ctx)
}

// Update replaces a set's description and entries. It returns nil when no set
// has the name. The audit record keeps the old entries.
func (s *Store) Update(ctx context.Context, name, description string, entries []SetEntry, actor string) (*Set, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("sets.Update: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	set, err := scanSet(tx.QueryRow(ctx, `SELECT `+setColumns+` FROM sets WHERE name = $1 FOR UPDATE`, name))
	if err != nil || set == nil {
		return nil, err
	}
	if err := s.loadEntries(ctx, tx, set); err != nil {
		return nil, err
	}
	old := set.Entries
	err = tx.QueryRow(ctx, `UPDATE sets SET description = $2, updated_at = now() WHERE id = $1 RETURNING updated_at`, set.ID, description).Scan(&set.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("sets.Update: %w", err)
	}
	if err := writeEntries(ctx, tx, set.ID, entries); err != nil {
		return nil, err
	}
	set.Description, set.Entries = description, entries
	if err := audit.Record(ctx, tx, actor, "set.update", "set:"+name, map[string]any{"old": old, "new": entries}); err != nil {
		return nil, err
	}
	if s.notifier != nil {
		if err := s.notifier.SetChanged(ctx, tx, set.ID); err != nil {
			return nil, err
		}
	} else if _, _, _, err := s.Recheck(ctx, tx, set.ID); err != nil {
		return nil, err
	}
	return set, tx.Commit(ctx)
}

// Delete removes a set and reports whether it existed.
func (s *Store) Delete(ctx context.Context, name, actor string) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("sets.Delete: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	tag, err := tx.Exec(ctx, `DELETE FROM sets WHERE name = $1`, name)
	if err != nil {
		return false, fmt.Errorf("sets.Delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	if err := audit.Record(ctx, tx, actor, "set.delete", "set:"+name, nil); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

// Resolve computes the set's current resolution through q. It stores nothing,
// so a caller can compare it inside a transaction.
func Resolve(ctx context.Context, q Querier, set *Set) (*Resolution, error) {
	res := &Resolution{Set: set.Name, Complete: true, Entries: []ResolvedEntry{}}
	for _, e := range set.Entries {
		entry, reason, err := resolveEntry(ctx, q, e)
		if err != nil {
			return nil, err
		}
		if reason != "" {
			res.Complete = false
			res.Excluded = append(res.Excluded, ExcludedEntry{Name: e.Name, Requested: requested(e), Reason: reason})
			continue
		}
		res.Entries = append(res.Entries, *entry)
	}
	slices.SortFunc(res.Entries, func(a, b ResolvedEntry) int { return strings.Compare(a.Name, b.Name) })
	res.ID = ResolutionID(res.Entries)
	return res, nil
}

func requested(e SetEntry) string {
	if e.Version == 0 {
		return "latest"
	}
	return fmt.Sprintf("%d", e.Version)
}

// resolveEntry applies the serving rule to one entry. A latest entry follows
// any alias. An exact pin follows only a rename, which keeps version numbers; a
// merge does not carry them across.
func resolveEntry(ctx context.Context, q Querier, e SetEntry) (*ResolvedEntry, string, error) {
	name := e.Name
	var latest int
	var removed bool
	err := q.QueryRow(ctx, `SELECT latest_version, removed_from_git_at IS NOT NULL FROM skills WHERE name = $1`,
		name).Scan(&latest, &removed)
	if errors.Is(err, pgx.ErrNoRows) {
		err = q.QueryRow(ctx, `
			SELECT s.name, s.latest_version, s.removed_from_git_at IS NOT NULL FROM skill_aliases a
			JOIN skills s ON s.name = a.canonical
			WHERE a.alias = $1 AND ($2 = 0 OR a.reason = 'rename')`, e.Name, e.Version).Scan(&name, &latest, &removed)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ReasonSkillNotFound, nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("sets: resolve %s: %w", e.Name, err)
	}

	version := e.Version
	if version == 0 {
		if latest == 0 {
			return nil, ReasonNoReleasedVersion, nil
		}
		version = latest
	}

	var checksum, gateState string
	var scanResult []byte
	var approved bool
	err = q.QueryRow(ctx, `
		SELECT v.checksum, v.gate_state, v.scan_result,
		       EXISTS (SELECT 1 FROM version_approvals a WHERE a.version_id = v.id AND a.decision = 'approved')
		FROM skill_versions v JOIN skills s ON s.id = v.skill_id
		WHERE s.name = $1 AND v.version = $2`, name, version).Scan(&checksum, &gateState, &scanResult, &approved)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ReasonVersionNotFound, nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("sets: resolve %s v%d: %w", e.Name, version, err)
	}
	switch gateState {
	case "released":
	case "rejected":
		return nil, ReasonRejected, nil
	default:
		return nil, ReasonHeld, nil
	}
	approval := "released"
	if approved {
		approval = "approved"
	}
	return &ResolvedEntry{
		Name: name, Version: version, SHA256: checksum,
		ScanVerdict: scanVerdict(scanResult), ApprovalState: approval, RemovedFromGit: removed,
	}, "", nil
}

func scanVerdict(raw []byte) string {
	var rep scan.Report
	if len(raw) == 0 || json.Unmarshal(raw, &rep) != nil || rep.Status == "" {
		return "not_scanned"
	}
	return rep.Status
}

// Recheck recomputes a set's resolution inside tx and reports whether its id
// differs from the last one recorded. The row lock serialises two changes to
// one set, so each sees the other's result. A changed id is saved in the same
// transaction, so a consumer told of it can fetch it at once.
func (s *Store) Recheck(ctx context.Context, tx pgx.Tx, setID string) (*Set, *Resolution, bool, error) {
	var previous *string
	set := &Set{ID: setID}
	err := tx.QueryRow(ctx, `SELECT name, resolution_id FROM sets WHERE id = $1 FOR UPDATE`, setID).Scan(&set.Name, &previous)
	if err != nil {
		return nil, nil, false, fmt.Errorf("sets.Recheck: %w", err)
	}
	if err := s.loadEntries(ctx, tx, set); err != nil {
		return nil, nil, false, err
	}
	res, err := Resolve(ctx, tx, set)
	if err != nil {
		return nil, nil, false, err
	}
	if previous != nil && *previous == res.ID {
		return set, res, false, nil
	}
	if _, err := tx.Exec(ctx, `UPDATE sets SET resolution_id = $2 WHERE id = $1`, setID, res.ID); err != nil {
		return nil, nil, false, fmt.Errorf("sets.Recheck: %w", err)
	}
	if err := saveResolution(ctx, tx, set, res); err != nil {
		return nil, nil, false, err
	}
	return set, res, previous != nil, nil
}

// SaveResolution stores a resolution. The row for an id never changes.
func (s *Store) SaveResolution(ctx context.Context, set *Set, res *Resolution) error {
	return saveResolution(ctx, s.pool, set, res)
}

func saveResolution(ctx context.Context, q Querier, set *Set, res *Resolution) error {
	raw, err := json.Marshal(res.Entries)
	if err != nil {
		return fmt.Errorf("sets.SaveResolution: %w", err)
	}
	_, err = q.Exec(ctx, `
		INSERT INTO set_resolutions (id, set_id, set_name, entries, complete)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (set_id, id) DO NOTHING`, res.ID, set.ID, set.Name, raw, res.Complete)
	if err != nil {
		return fmt.Errorf("sets.SaveResolution: %w", err)
	}
	return nil
}

// GetResolution returns a stored resolution, or nil.
func (s *Store) GetResolution(ctx context.Context, set *Set, id string) (*Resolution, error) {
	res := Resolution{ID: id, Set: set.Name}
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT entries, complete FROM set_resolutions WHERE set_id = $1 AND id = $2`, set.ID, id).Scan(&raw, &res.Complete)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sets.GetResolution: %w", err)
	}
	if err := json.Unmarshal(raw, &res.Entries); err != nil {
		return nil, fmt.Errorf("sets.GetResolution: %w", err)
	}
	return &res, nil
}

// RecordCaller notes the last use of a set by a key's identity.
func (s *Store) RecordCaller(ctx context.Context, set *Set, userID, name, resolutionID string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO set_callers (set_id, user_id, caller_name, resolution_id)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (set_id, user_id) DO UPDATE
		SET caller_name = $3, resolution_id = $4, last_seen_at = now()`, set.ID, userID, name, resolutionID)
	if err != nil {
		return fmt.Errorf("sets.RecordCaller: %w", err)
	}
	return nil
}

// Callers lists who last used a set, newest first.
func (s *Store) Callers(ctx context.Context, set *Set) ([]SetCaller, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT caller_name, resolution_id, last_seen_at FROM set_callers
		WHERE set_id = $1 ORDER BY last_seen_at DESC`, set.ID)
	if err != nil {
		return nil, fmt.Errorf("sets.Callers: %w", err)
	}
	callers, err := pgx.CollectRows(rows, pgx.RowToStructByPos[SetCaller])
	if err != nil {
		return nil, fmt.Errorf("sets.Callers: %w", err)
	}
	if callers == nil {
		callers = []SetCaller{}
	}
	return callers, nil
}

// ArchivePath returns where a resolved version's archive lives, or "" when its
// skill or version has gone.
func (s *Store) ArchivePath(ctx context.Context, name string, version int) (string, error) {
	var path string
	err := s.pool.QueryRow(ctx, `
		SELECT v.archive_path FROM skill_versions v JOIN skills s ON s.id = v.skill_id
		WHERE s.name = $1 AND v.version = $2`, name, version).Scan(&path)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("sets.ArchivePath: %w", err)
	}
	return path, nil
}

// SkillVersionExists supports validating an entry at edit time.
func (s *Store) SkillVersionExists(ctx context.Context, name string, version int) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM skills s
			LEFT JOIN skill_versions v ON v.skill_id = s.id AND v.version = $2
			WHERE s.name = $1 AND ($2 = 0 OR v.id IS NOT NULL))
		OR EXISTS (SELECT 1 FROM skill_aliases a
			JOIN skills s ON s.name = a.canonical
			LEFT JOIN skill_versions v ON v.skill_id = s.id AND v.version = $2
			WHERE a.alias = $1 AND ($2 = 0 OR (a.reason = 'rename' AND v.id IS NOT NULL)))`, name, version).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("sets.SkillVersionExists: %w", err)
	}
	return ok, nil
}
