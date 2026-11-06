package gitsync

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog/log"

	"github.com/skael-dev/skael/internal/audit"
	"github.com/skael-dev/skael/internal/auth"
	"github.com/skael-dev/skael/internal/skill"
)

// Change is an outbound change: the pull request Skael opened for a publish
// to a git-bound skill.
type Change struct {
	ID           string    `json:"id"`
	ConnectionID string    `json:"connection_id"`
	SkillID      string    `json:"skill_id"`
	SkillName    string    `json:"skill_name"`
	Branch       string    `json:"branch"`
	PRNumber     int       `json:"pr_number"`
	PRURL        string    `json:"pr_url"`
	HeadSHA      string    `json:"head_sha"`
	Digest       string    `json:"digest"`
	RequestedBy  string    `json:"requested_by"`
	State        string    `json:"state"`
	MergeSHA     string    `json:"merge_sha,omitempty"`
	Version      *int      `json:"version,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

const changeColumns = `c.id, c.connection_id, c.skill_id, s.name, c.branch, c.pr_number, c.pr_url, c.head_sha,
	c.digest, c.requested_by, c.state, c.merge_sha, c.version, c.created_at, c.updated_at`

func (s *Store) changes(ctx context.Context, where string, args ...any) ([]Change, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+changeColumns+` FROM git_changes c JOIN skills s ON s.id = c.skill_id
		WHERE `+where+` ORDER BY c.created_at DESC`, args...)
	if err != nil {
		return nil, fmt.Errorf("gitsync: changes: %w", err)
	}
	defer rows.Close()
	out := []Change{}
	for rows.Next() {
		var c Change
		if err := rows.Scan(&c.ID, &c.ConnectionID, &c.SkillID, &c.SkillName, &c.Branch, &c.PRNumber, &c.PRURL,
			&c.HeadSHA, &c.Digest, &c.RequestedBy, &c.State, &c.MergeSHA, &c.Version, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, fmt.Errorf("gitsync: changes: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ChangesForSkill returns a skill's outbound changes, newest first.
func (s *Store) ChangesForSkill(ctx context.Context, name string) ([]Change, error) {
	return s.changes(ctx, `s.name = $1`, name)
}

// Bound reports whether git owns a skill, and names its repo path.
func (s *Syncer) Bound(ctx context.Context, name string) (bool, string, error) {
	b, err := s.store.BindingForSkill(ctx, name)
	if err != nil || b == nil {
		return false, "", err
	}
	c, err := s.store.Connection(ctx, b.ConnectionID)
	if err != nil || c == nil {
		return false, "", err
	}
	return true, c.Repo() + "/" + b.Path, nil
}

var branchSafe = regexp.MustCompile(`[^a-z0-9._-]+`)

// OpenChange commits archive to a new branch and opens a pull request for the
// git-bound skill name, on behalf of the person in ctx. The requester needs no
// GitHub access: the App authors the commit and the trailer names them.
func (s *Syncer) OpenChange(ctx context.Context, name string, archive []byte, note string) (*skill.OutboundChange, error) {
	u := auth.UserFromContext(ctx)
	if u == nil {
		return nil, errors.New("gitsync: open a change: no user")
	}
	b, err := s.store.BindingForSkill(ctx, name)
	if err != nil {
		return nil, err
	}
	if b == nil {
		return nil, fmt.Errorf("gitsync: %s is not git-bound", name)
	}
	c, err := s.store.Connection(ctx, b.ConnectionID)
	if err != nil || c == nil {
		return nil, fmt.Errorf("gitsync: the connection of %s: %w", name, err)
	}
	gh, err := s.github(ctx)
	if err != nil {
		return nil, mapGitHubForUser(err)
	}

	dir, err := os.MkdirTemp("", "skael-change-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	if err := skill.Unpack(bytes.NewReader(archive), dir); err != nil {
		return nil, userErrorf("invalid archive: %s", err)
	}
	_, digest, _, err := skill.Pack(dir)
	if err != nil {
		return nil, userErrorf("the archive does not pack: %s", err)
	}
	var files []FolderFile
	if err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		content, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		files = append(files, FolderFile{Path: filepath.ToSlash(rel), Content: content, Executable: info.Mode()&0o111 != 0})
		return nil
	}); err != nil {
		return nil, err
	}

	id := uuid.New().String()
	branch := "skael/" + strings.Trim(branchSafe.ReplaceAllString(strings.ToLower(name), "-"), "-") + "/" + id[:8]
	who := u.Name
	if who == "" {
		who = u.Email
	}
	msg := "Update " + name + " from Skael\n\n"
	if note != "" {
		msg += note + "\n\n"
	}
	msg += fmt.Sprintf("Co-authored-by: %s <%s>\n", who, u.Email)
	head, err := gh.CommitFolder(ctx, c.InstallationID, c.RepoOwner, c.RepoName, c.Branch, branch, b.Path, files, msg)
	if err != nil {
		return nil, mapGitHubForUser(err)
	}
	body := fmt.Sprintf("Requested in Skael by %s (%s).\n\n", who, u.Email)
	if note != "" {
		body += note + "\n\n"
	}
	body += fmt.Sprintf("Skael publishes the next version of `%s` when this pull request merges.", name)
	pr, err := gh.OpenPullRequest(ctx, c.InstallationID, c.RepoOwner, c.RepoName, branch, c.Branch, "Update "+name, body)
	if err != nil {
		_ = gh.DeleteBranch(ctx, c.InstallationID, c.RepoOwner, c.RepoName, branch)
		return nil, mapGitHubForUser(err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		INSERT INTO git_changes (id, connection_id, skill_id, branch, pr_number, pr_url, head_sha, digest, requested_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		id, c.ID, b.SkillID, branch, pr.Number, pr.HTMLURL, head, digest, u.Email); err != nil {
		return nil, fmt.Errorf("gitsync: record the change: %w", err)
	}
	if err := audit.Record(ctx, tx, u.Email, "git_change.open", "skill:"+name, map[string]any{
		"change": id, "pr": pr.HTMLURL, "repo": c.Repo(),
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	log.Info().Str("skill", name).Str("pr", pr.HTMLURL).Str("requested_by", u.Email).Msg("gitsync: opened a change")
	return &skill.OutboundChange{ID: id, PRNumber: pr.Number, PRURL: pr.HTMLURL, State: "open"}, nil
}

// mapGitHubForUser turns a GitHub failure on a person's request into an answer
// they can act on.
func mapGitHubForUser(err error) error {
	if unreachable(err) {
		return userErrorf("GitHub refused the change: %v. Check that the App is still installed on the repo", err)
	}
	return err
}

// trackChanges reads each open change's pull request. A closed one is closed
// in Skael, its submission rejected and its branch deleted. A merged one waits
// for the sync that publishes the merged folder.
func (r *run) trackChanges(ctx context.Context, gh *GitHub) error {
	open, err := r.s.store.changes(ctx, `c.connection_id = $1 AND c.state = 'open'`, r.c.ID)
	if err != nil {
		return err
	}
	for _, ch := range open {
		pr, err := gh.GetPullRequest(ctx, r.c.InstallationID, r.c.RepoOwner, r.c.RepoName, ch.PRNumber)
		if err != nil {
			if unreachable(err) {
				continue
			}
			return err
		}
		switch {
		case pr.Merged:
			if _, err := r.s.pool.Exec(ctx, `UPDATE git_changes SET state = 'merged', merge_sha = $2, updated_at = now()
				WHERE id = $1`, ch.ID, pr.MergeCommitSHA); err != nil {
				return err
			}
		case pr.State == "closed":
			if _, err := r.s.pool.Exec(ctx, `UPDATE git_changes SET state = 'closed', updated_at = now() WHERE id = $1`, ch.ID); err != nil {
				return err
			}
			if r.s.submissions != nil {
				if err := r.s.submissions.ResolveChange(ctx, ch.ID, false, nil, r.saEmail, "PR closed unmerged"); err != nil {
					return err
				}
			}
			if err := gh.DeleteBranch(ctx, r.c.InstallationID, r.c.RepoOwner, r.c.RepoName, ch.Branch); err != nil {
				log.Warn().Err(err).Str("branch", ch.Branch).Msg("gitsync: delete a closed change's branch")
			}
		}
	}
	return nil
}

// linkMerged gives each merged change the version its merge produced: the one
// with the change's digest, or else the skill's version after this sync, when
// the merge changed the folder. Its submission is approved with that version.
func (r *run) linkMerged(ctx context.Context, gh *GitHub) error {
	merged, err := r.s.store.changes(ctx, `c.connection_id = $1 AND c.state = 'merged' AND c.version IS NULL`, r.c.ID)
	if err != nil {
		return err
	}
	for _, ch := range merged {
		var version int
		err := r.s.pool.QueryRow(ctx, `SELECT version FROM skill_versions WHERE skill_id = $1 AND checksum = $2
			ORDER BY version DESC LIMIT 1`, ch.SkillID, ch.Digest).Scan(&version)
		if errors.Is(err, pgx.ErrNoRows) {
			err = r.s.pool.QueryRow(ctx, `SELECT COALESCE(MAX(version), 0) FROM skill_versions WHERE skill_id = $1`,
				ch.SkillID).Scan(&version)
		}
		if err != nil {
			return err
		}
		if version == 0 {
			continue
		}
		if _, err := r.s.pool.Exec(ctx, `UPDATE git_changes SET version = $2, updated_at = now() WHERE id = $1`, ch.ID, version); err != nil {
			return err
		}
		if r.s.submissions != nil {
			v := version
			if err := r.s.submissions.ResolveChange(ctx, ch.ID, true, &v, ch.RequestedBy, ""); err != nil {
				return err
			}
		}
		if err := gh.DeleteBranch(ctx, r.c.InstallationID, r.c.RepoOwner, r.c.RepoName, ch.Branch); err != nil {
			log.Warn().Err(err).Str("branch", ch.Branch).Msg("gitsync: delete a merged change's branch")
		}
		log.Info().Str("skill", ch.SkillName).Int("version", version).Str("pr", ch.PRURL).Msg("gitsync: a change merged")
	}
	return nil
}
