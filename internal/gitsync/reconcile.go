package gitsync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/rs/zerolog/log"

	"github.com/skael-dev/skael/internal/audit"
	"github.com/skael-dev/skael/internal/auth"
	skillimport "github.com/skael-dev/skael/internal/import"
	"github.com/skael-dev/skael/internal/ownership"
	"github.com/skael-dev/skael/internal/skill"
)

// unreachable reports whether err means the repo, branch or installation is
// gone or closed to the App. Retrying cannot fix that; a person must.
func unreachable(err error) bool {
	var apiErr *APIError
	return errors.Is(err, ErrNotFound) || errors.Is(err, ErrNoApp) ||
		(errors.As(err, &apiErr) && (apiErr.Status == http.StatusUnauthorized || apiErr.Status == http.StatusForbidden))
}

// Reconcile brings one connection in step with its branch head. A connection
// that cannot reach its repo is marked errored and no skill changes. force
// syncs even when the head has not moved, after an adoption.
func (s *Syncer) Reconcile(ctx context.Context, id string, force bool) error {
	if s.publisher == nil {
		return errors.New("gitsync: the Syncer has no publish path; Bind was not called")
	}
	c, err := s.store.Connection(ctx, id)
	if err != nil || c == nil {
		return err
	}
	gh, err := s.github(ctx)
	if err != nil {
		if unreachable(err) {
			return s.store.recordError(ctx, id, err.Error())
		}
		return err
	}
	head, err := gh.BranchHead(ctx, c.InstallationID, c.RepoOwner, c.RepoName, c.Branch)
	if err != nil {
		if unreachable(err) {
			return s.store.recordError(ctx, id, fmt.Sprintf("cannot reach branch %s of %s: %v", c.Branch, c.Repo(), err))
		}
		return err
	}
	r := &run{s: s, c: c}
	if err := s.pool.QueryRow(ctx, `SELECT email FROM auth_users WHERE id = $1`, c.ServiceAccountID).Scan(&r.saEmail); err != nil {
		return fmt.Errorf("gitsync: load the connection's service account: %w", err)
	}
	// A pull request can close without the branch moving, so changes are
	// read on every tick.
	if err := r.trackChanges(ctx, gh); err != nil {
		return err
	}
	if err := r.checkPulls(ctx, gh); err != nil {
		return err
	}
	if head == c.LastSyncedSHA && c.Status == "ok" && !force {
		_, err := s.pool.Exec(ctx, `UPDATE git_connections SET last_sync_at = now() WHERE id = $1`, id)
		return err
	}
	sn, err := s.fetch(ctx, gh, c.InstallationID, c.RepoOwner, c.RepoName, head, c.RootPath)
	if err != nil {
		// A missing root path or an unreadable archive needs a person, and the
		// skills stay as they are until one acts.
		return s.store.recordError(ctx, id, err.Error())
	}
	defer sn.close()

	author, err := gh.CommitAuthor(ctx, c.InstallationID, c.RepoOwner, c.RepoName, head)
	if err != nil {
		log.Warn().Err(err).Str("connection", id).Msg("gitsync: commit author lookup failed")
	}
	r.sn, r.author = sn, author
	if err := r.apply(ctx); err != nil {
		return err
	}
	if err := r.linkMerged(ctx, gh); err != nil {
		return err
	}
	return s.store.recordSync(ctx, id, head, r.refusals)
}

// run is one sync of one connection.
type run struct {
	s        *Syncer
	c        *Connection
	sn       *snapshot
	author   string
	saEmail  string
	refusals []Refusal
	actor    context.Context
}

func (r *run) refuse(f skillimport.DiscoveredSkill, reason string) {
	r.refusals = append(r.refusals, Refusal{Path: f.Path, SkillName: f.Name, Reason: reason})
}

func (r *run) apply(ctx context.Context) error {
	r.actor = auth.ContextWithUser(ctx, &auth.User{
		ID: r.c.ServiceAccountID, Email: r.saEmail, Name: strings.TrimSuffix(r.saEmail, "@service-account.invalid"),
		Role: auth.RoleMember, Kind: auth.KindService,
	})

	bindings, err := r.s.store.Bindings(ctx, r.c.ID)
	if err != nil {
		return err
	}
	ignored, err := r.s.store.Ignored(ctx, r.c.ID)
	if err != nil {
		return err
	}

	counts := map[string]int{}
	byPath := map[string]skillimport.DiscoveredSkill{}
	for _, f := range r.sn.folders {
		counts[f.Name]++
		byPath[f.Path] = f
	}
	accepted := func(f skillimport.DiscoveredSkill) bool {
		return counts[f.Name] == 1 && !ignored[f.Name] && ownership.Matches(r.c.Prefix, f.Name)
	}

	bound := map[string]*Binding{}
	for i := range bindings {
		b := &bindings[i]
		if counts[b.SkillName] == 0 {
			if f, ok := byPath[b.Path]; ok && accepted(f) {
				renamed, err := r.rename(ctx, b, f)
				if err != nil {
					return err
				}
				if renamed {
					b.SkillName = f.Name
				}
			}
		}
		bound[b.SkillName] = b
	}

	for _, f := range r.sn.folders {
		switch {
		case ignored[f.Name]:
			continue
		case !ownership.Matches(r.c.Prefix, f.Name):
			r.refuse(f, "outside the prefix "+r.c.Prefix)
			continue
		case counts[f.Name] > 1:
			r.refuse(f, "another folder in the repo declares this name")
			continue
		}
		if err := r.sync(ctx, f, bound[f.Name]); err != nil {
			return err
		}
	}

	for name, b := range bound {
		if counts[name] > 0 || b.RemovedAt != nil {
			continue
		}
		if _, err := r.s.pool.Exec(ctx, `UPDATE skills SET removed_from_git_at = now(), removed_from_git_commit = $2
			WHERE id = $1`, b.SkillID, r.sn.sha); err != nil {
			return fmt.Errorf("gitsync: mark %s removed: %w", name, err)
		}
		log.Info().Str("connection", r.c.ID).Str("skill", name).Msg("gitsync: skill folder left the repo; its last version stays served")
	}
	return nil
}

// rename renames a bound skill whose folder now declares a new, free name, and
// keeps the old name as a rename alias, which an exact pin follows.
func (r *run) rename(ctx context.Context, b *Binding, f skillimport.DiscoveredSkill) (bool, error) {
	taken, err := r.s.skills.GetByName(ctx, f.Name)
	if err != nil {
		return false, err
	}
	if taken != nil {
		return false, nil
	}
	tx, err := r.s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `UPDATE skills SET name = $2 WHERE id = $1`, b.SkillID, f.Name); err != nil {
		return false, fmt.Errorf("gitsync: rename %s: %w", b.SkillName, err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO skill_aliases (alias, canonical, reason) VALUES ($1, $2, 'rename')
		ON CONFLICT (alias) DO UPDATE SET canonical = EXCLUDED.canonical, reason = 'rename'`,
		b.SkillName, f.Name); err != nil {
		return false, fmt.Errorf("gitsync: alias %s: %w", b.SkillName, err)
	}
	if err := audit.Record(ctx, tx, r.c.CreatedBy, "git_sync.rename", "skill:"+f.Name, map[string]any{
		"from": b.SkillName, "connection": r.c.ID, "commit": r.sn.sha, "author": r.author,
	}); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	log.Info().Str("connection", r.c.ID).Str("from", b.SkillName).Str("to", f.Name).Msg("gitsync: renamed a skill")
	return true, nil
}

// sync brings one accepted folder into the registry.
func (r *run) sync(ctx context.Context, f skillimport.DiscoveredSkill, b *Binding) error {
	created := false
	if b == nil {
		sk, err := r.s.skills.GetByName(ctx, f.Name)
		if err != nil {
			return err
		}
		if sk != nil {
			other, err := r.s.store.BindingForSkill(ctx, f.Name)
			if err != nil {
				return err
			}
			if other != nil {
				r.refuse(f, "the skill already belongs to another connection")
			} else {
				r.refuse(f, "name taken by a skill in the registry; adopt it on this connection's page")
			}
			return nil
		}
		if _, err := r.s.skills.Create(ctx, f.Name, "", f.Description, "", json.RawMessage(`{}`)); err != nil {
			return fmt.Errorf("gitsync: create %s: %w", f.Name, err)
		}
		created = true
	}

	dir := filepath.Join(r.sn.dir, filepath.FromSlash(f.Path))
	raw, _, _, err := skill.Pack(dir)
	if err != nil {
		return r.undo(ctx, f, created, "the folder does not pack: "+err.Error())
	}
	res, err := r.s.publisher.Publish(r.actor, f.Name, raw, false)
	if err != nil {
		var se huma.StatusError
		if errors.As(err, &se) && se.GetStatus() < 500 {
			return r.undo(ctx, f, created, se.Error())
		}
		return fmt.Errorf("gitsync: publish %s: %w", f.Name, err)
	}

	if _, err := r.s.pool.Exec(ctx, `
		INSERT INTO git_bindings (skill_id, connection_id, path, last_commit)
		SELECT id, $2, $3, $4 FROM skills WHERE name = $1
		ON CONFLICT (skill_id) DO UPDATE SET path = EXCLUDED.path, last_commit = EXCLUDED.last_commit`,
		f.Name, r.c.ID, f.Path, r.sn.sha); err != nil {
		return fmt.Errorf("gitsync: bind %s: %w", f.Name, err)
	}
	if _, err := r.s.pool.Exec(ctx, `UPDATE skills SET removed_from_git_at = NULL, removed_from_git_commit = ''
		WHERE name = $1 AND removed_from_git_at IS NOT NULL`, f.Name); err != nil {
		return err
	}
	if res.Created {
		if err := r.linkContest(ctx, res.ID, res.SkillID, res.Checksum); err != nil {
			return fmt.Errorf("gitsync: link the PR contest of %s: %w", f.Name, err)
		}
		if err := audit.Record(ctx, r.s.pool, res.PublishedBy, "git_sync.publish", "skill:"+f.Name, map[string]any{
			"version": res.Version.Version, "connection": r.c.ID, "repo": r.c.Repo(),
			"commit": r.sn.sha, "author": r.author, "gate_state": res.GateState,
		}); err != nil {
			return err
		}
		log.Info().Str("connection", r.c.ID).Str("skill", f.Name).Int("version", res.Version.Version).
			Str("commit", short(r.sn.sha)).Msg("gitsync: published a version from git")
	}
	return nil
}

// undo refuses a folder the publish path would not take, and removes a skill
// row this sync created for it, so a refused new folder leaves nothing behind.
func (r *run) undo(ctx context.Context, f skillimport.DiscoveredSkill, created bool, reason string) error {
	r.refuse(f, reason)
	if !created {
		return nil
	}
	sk, err := r.s.skills.GetByName(ctx, f.Name)
	if err != nil || sk == nil || sk.LatestVersion > 0 {
		return err
	}
	var versions int
	if err := r.s.pool.QueryRow(ctx, `SELECT count(*) FROM skill_versions WHERE skill_id = $1`, sk.ID).Scan(&versions); err != nil {
		return err
	}
	if versions == 0 {
		return r.s.skills.Delete(ctx, f.Name)
	}
	return nil
}
