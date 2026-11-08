package gitsync

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/skael-dev/skael/internal/audit"
	"github.com/skael-dev/skael/internal/auth"
	"github.com/skael-dev/skael/internal/contest"
	"github.com/skael-dev/skael/internal/evalsuite"
	skillimport "github.com/skael-dev/skael/internal/import"
	"github.com/skael-dev/skael/internal/ownership"
	"github.com/skael-dev/skael/internal/platform"
	"github.com/skael-dev/skael/internal/skill"
	"github.com/skael-dev/skael/internal/submissions"
)

// ErrNoApp means the instance has no GitHub App yet.
var ErrNoApp = errors.New("connect GitHub first: this instance has no GitHub App")

// Syncer keeps connections in step with their repositories.
type Syncer struct {
	store  *Store
	pool   *pgxpool.Pool
	client *http.Client

	// skills and publisher are the registry's own publish path, bound once
	// the routes build it, so a sync meets the same scan and gate as a
	// person's publish.
	skills      *skill.Store
	publisher   *skill.Publisher
	submissions *submissions.Store
	deps        Deps

	mu       sync.Mutex
	gh       *GitHub
	ghAppKey string
}

// NewSyncer builds a Syncer. Bind must run before any sync.
func NewSyncer(store *Store, client *http.Client) *Syncer {
	if client == nil {
		client = &http.Client{}
	}
	return &Syncer{store: store, pool: store.pool, client: client}
}

// Store returns the Syncer's store.
func (s *Syncer) Store() *Store { return s.store }

// Bind hands the Syncer the registry's publish path. The River worker is built
// before the routes build that path, so the two meet here.
func (s *Syncer) Bind(d Deps) {
	s.skills, s.publisher, s.submissions, s.deps = d.Skills, d.Publisher, d.Submissions, d
}

// Deps are the registry's parts a sync and a PR check use.
type Deps struct {
	Skills      *skill.Store
	Publisher   *skill.Publisher
	Submissions *submissions.Store
	// Contests and Queue start a contest for a PR head. Nil leaves PR checks
	// at the scan.
	Contests *contest.Store
	Queue    contest.Submitter
	// Suites names the suite a PR contest runs: the skill's latest.
	Suites *evalsuite.Registry
	// Storage holds PR head archives.
	Storage platform.Storage
	// Owners and Users decide whether an outbound change's requester owns the
	// skill, for the PR check.
	Owners skill.OwnerResolver
	Users  *auth.UserStore
	// PublicURL links a check run to Skael's contest page. It is read when
	// the check run is written.
	PublicURL func() string
}

// github returns a client for the stored App, rebuilt when the App changes.
func (s *Syncer) github(ctx context.Context) (*GitHub, error) {
	app, err := s.store.App(ctx)
	if err != nil {
		return nil, err
	}
	if app == nil {
		return nil, ErrNoApp
	}
	key := fmt.Sprintf("%d/%d", app.AppID, app.CreatedAt.UnixNano())
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gh != nil && s.ghAppKey == key {
		return s.gh, nil
	}
	gh, err := NewGitHub(app, s.client)
	if err != nil {
		return nil, err
	}
	s.gh, s.ghAppKey = gh, key
	return gh, nil
}

// snapshot is one commit of a repo, unpacked.
type snapshot struct {
	dir     string
	sha     string
	folders []skillimport.DiscoveredSkill
}

func (s *Syncer) fetch(ctx context.Context, gh *GitHub, installationID int64, owner, repo, sha, root string) (*snapshot, error) {
	token, err := gh.Token(ctx, installationID)
	if err != nil {
		return nil, err
	}
	res, err := skillimport.NewFetcher(gh.app.APIBaseURL, token).Fetch(skillimport.Source{Owner: owner, Repo: repo, CommitSHA: sha})
	if err != nil {
		return nil, fmt.Errorf("gitsync: download %s/%s at %s: %w", owner, repo, short(sha), err)
	}
	if root != "" {
		info, err := os.Stat(filepath.Join(res.Dir, filepath.FromSlash(root)))
		if err != nil || !info.IsDir() {
			_ = os.RemoveAll(res.Dir)
			return nil, userErrorf("the root path %q does not exist at %s", root, short(sha))
		}
	}
	folders, err := skillimport.DiscoverAll(res.Dir, root)
	if err != nil {
		_ = os.RemoveAll(res.Dir)
		return nil, fmt.Errorf("gitsync: discover skills: %w", err)
	}
	return &snapshot{dir: res.Dir, sha: sha, folders: folders}, nil
}

func (sn *snapshot) close() { _ = os.RemoveAll(sn.dir) }

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// CreateRequest is a connection as a person asks for it.
type CreateRequest struct {
	RepoOwner   string
	RepoName    string
	Branch      string
	RootPath    string
	Prefix      string
	ContestMode string
}

func (r *CreateRequest) normalize() error {
	r.RepoOwner, r.RepoName = strings.TrimSpace(r.RepoOwner), strings.TrimSpace(r.RepoName)
	r.Branch = strings.TrimSpace(r.Branch)
	r.RootPath = strings.Trim(strings.TrimSpace(r.RootPath), "/")
	r.Prefix = strings.TrimSpace(r.Prefix)
	if r.ContestMode == "" {
		r.ContestMode = ContestOnRequest
	}
	switch {
	case r.RepoOwner == "" || r.RepoName == "" || r.Branch == "":
		return userErrorf("name the repo and the branch")
	case strings.Contains(r.RootPath, ".."):
		return userErrorf("the root path cannot leave the repo")
	case !ownership.IsPrefix(r.Prefix):
		return userErrorf("the prefix must end in *, e.g. platform:*")
	case r.ContestMode != ContestOff && r.ContestMode != ContestOnRequest && r.ContestMode != ContestReady:
		return userErrorf("unknown contest setting %q", r.ContestMode)
	}
	if err := ownership.ValidatePattern(r.Prefix); err != nil {
		return userErrorf("%s", err.Error())
	}
	return nil
}

// Preview kinds.
const (
	KindAdopt   = "adopt"   // imported earlier from this repo and path
	KindMatch   = "match"   // same name, another origin
	KindNew     = "new"     // a name the registry does not have
	KindRefused = "refused" // outside the prefix, a duplicate, or bound elsewhere
)

// PreviewItem is one folder and what creating the connection does with it.
type PreviewItem struct {
	Path                string             `json:"path"`
	Name                string             `json:"name"`
	Description         string             `json:"description"`
	Kind                string             `json:"kind"`
	Reason              string             `json:"reason,omitempty"`
	ExistingDescription string             `json:"existing_description,omitempty"`
	ExistingVersion     int                `json:"existing_version,omitempty"`
	Diff                *skill.VersionDiff `json:"diff,omitempty"`
}

// Preview is what creating a connection would do, at the branch head.
type Preview struct {
	InstallationID int64         `json:"installation_id"`
	HeadSHA        string        `json:"head_sha"`
	Items          []PreviewItem `json:"items"`
}

func actorOf(u *auth.User) ownership.Actor {
	return ownership.Actor{UserID: u.ID, Privileged: u.IsPrivileged()}
}

// ErrForbidden means the person may not change the ownership rule for the prefix.
var ErrForbidden = errors.New("only an admin or an owner of the prefix can connect a repo to it")

func (s *Syncer) authorize(ctx context.Context, u *auth.User, prefix string) error {
	rules, err := ownership.NewStore(s.pool).List(ctx)
	if err != nil {
		return err
	}
	if !ownership.CanManage(actorOf(u), prefix, rules) {
		return ErrForbidden
	}
	return nil
}

// Preview classifies every skill folder at the branch head.
func (s *Syncer) Preview(ctx context.Context, u *auth.User, req CreateRequest) (*Preview, error) {
	if err := req.normalize(); err != nil {
		return nil, err
	}
	if err := s.authorize(ctx, u, req.Prefix); err != nil {
		return nil, err
	}
	gh, err := s.github(ctx)
	if err != nil {
		return nil, err
	}
	inst, err := gh.RepoInstallation(ctx, req.RepoOwner, req.RepoName)
	if errors.Is(err, ErrNotFound) {
		return nil, userErrorf("the GitHub App is not installed on %s/%s; install it there first", req.RepoOwner, req.RepoName)
	}
	if err != nil {
		return nil, err
	}
	head, err := gh.BranchHead(ctx, inst, req.RepoOwner, req.RepoName, req.Branch)
	if errors.Is(err, ErrNotFound) {
		return nil, userErrorf("branch %q does not exist on %s/%s", req.Branch, req.RepoOwner, req.RepoName)
	}
	if err != nil {
		return nil, err
	}
	sn, err := s.fetch(ctx, gh, inst, req.RepoOwner, req.RepoName, head, req.RootPath)
	if err != nil {
		return nil, err
	}
	defer sn.close()

	p := &Preview{InstallationID: inst, HeadSHA: head, Items: []PreviewItem{}}
	counts := map[string]int{}
	for _, f := range sn.folders {
		counts[f.Name]++
	}
	origin := "https://github.com/" + req.RepoOwner + "/" + req.RepoName
	for _, f := range sn.folders {
		item := PreviewItem{Path: f.Path, Name: f.Name, Description: f.Description}
		switch {
		case !ownership.Matches(req.Prefix, f.Name):
			item.Kind, item.Reason = KindRefused, "outside the prefix "+req.Prefix
		case counts[f.Name] > 1:
			item.Kind, item.Reason = KindRefused, "another folder in the repo declares this name"
		default:
			if err := s.classifyExisting(ctx, &item, sn, f, origin); err != nil {
				return nil, err
			}
		}
		p.Items = append(p.Items, item)
	}
	return p, nil
}

// classifyExisting sorts a folder by what the registry already holds under
// its name.
func (s *Syncer) classifyExisting(ctx context.Context, item *PreviewItem, sn *snapshot, f skillimport.DiscoveredSkill, origin string) error {
	sk, err := s.skills.GetByName(ctx, f.Name)
	if err != nil {
		return err
	}
	if sk == nil {
		item.Kind = KindNew
		return nil
	}
	if b, err := s.store.BindingForSkill(ctx, f.Name); err != nil {
		return err
	} else if b != nil {
		item.Kind, item.Reason = KindRefused, "the skill already belongs to another connection"
		return nil
	}
	item.ExistingDescription, item.ExistingVersion = sk.Description, sk.LatestVersion
	var fromHere bool
	if err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM import_sources
			WHERE skill_id = $1 AND lower(source_url) = lower($2) AND source_path = $3)`,
		sk.ID, origin, f.Path).Scan(&fromHere); err != nil {
		return fmt.Errorf("gitsync: import origin: %w", err)
	}
	item.Kind = KindMatch
	if fromHere {
		item.Kind = KindAdopt
	}
	if sk.LatestVersion > 0 {
		served, err := s.skills.GetVersion(ctx, f.Name, sk.LatestVersion)
		if err != nil {
			return err
		}
		if served != nil {
			diff, err := folderDiff(served, filepath.Join(sn.dir, filepath.FromSlash(f.Path)), "git")
			if err != nil {
				return err
			}
			item.Diff = diff
		}
	}
	return nil
}

func folderDiff(served *skill.Version, dir, label string) (*skill.VersionDiff, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		return nil, err
	}
	_, body, err := skill.ParseFrontmatter(string(raw))
	if err != nil {
		return nil, err
	}
	_, _, files, err := skill.Pack(dir)
	if err != nil {
		return nil, err
	}
	return skill.DiffFolder(served, body, files, label)
}

var accountNameRe = regexp.MustCompile(`[^a-z0-9-]+`)

// serviceAccountName derives a unique, readable name such as git-acme-skills-3f9a.
func serviceAccountName(owner, repo string) (string, error) {
	base := strings.Trim(accountNameRe.ReplaceAllString(strings.ToLower("git-"+owner+"-"+repo), "-"), "-")
	if len(base) > 58 {
		base = strings.Trim(base[:58], "-")
	}
	b := make([]byte, 2)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base + "-" + hex.EncodeToString(b), nil
}

// Create makes the connection, its service account and its ownership, and
// binds the adopted skills. adopt names the folders to adopt: any from the
// adopt or match groups of a fresh preview.
func (s *Syncer) Create(ctx context.Context, u *auth.User, req CreateRequest, adopt []string) (*Connection, error) {
	p, err := s.Preview(ctx, u, req)
	if err != nil {
		return nil, err
	}
	_ = req.normalize()
	adoptable := map[string]bool{}
	for _, it := range p.Items {
		if it.Kind == KindAdopt || it.Kind == KindMatch {
			adoptable[it.Name] = true
		}
	}
	for _, name := range adopt {
		if !adoptable[name] {
			return nil, userErrorf("%q is not a skill this connection can adopt", name)
		}
	}

	saName, err := serviceAccountName(req.RepoOwner, req.RepoName)
	if err != nil {
		return nil, err
	}
	rules, err := ownership.NewStore(s.pool).List(ctx)
	if err != nil {
		return nil, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	sa, err := auth.CreateServiceAccountTx(ctx, tx, saName, u.Email)
	if err != nil {
		return nil, err
	}
	// The prefix rule gains the connection. A new rule keeps whoever owned the
	// prefix through an enclosing rule, because the longest match replaces it;
	// with no enclosing rule, the person connecting owns it.
	members := []string{sa.ID}
	if existing := findRule(rules, req.Prefix); existing == nil {
		res := ownership.Resolve(strings.TrimSuffix(req.Prefix, "*"), rules)
		if res.Unowned() {
			members = append(members, u.ID)
		} else {
			members = append(members, res.Members...)
		}
	}
	if err := ownership.AddMembersTx(ctx, tx, req.Prefix, members, u.ID); err != nil {
		return nil, err
	}

	c, err := scanConnection(tx.QueryRow(ctx, `
		INSERT INTO git_connections (installation_id, repo_owner, repo_name, branch, root_path, prefix,
			contest_mode, service_account_id, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING `+connectionColumns,
		p.InstallationID, req.RepoOwner, req.RepoName, req.Branch, req.RootPath, req.Prefix,
		req.ContestMode, sa.ID, u.Email))
	if err != nil {
		if strings.Contains(err.Error(), "git_connections_repo_owner_repo_name_branch_root_path_key") {
			return nil, userErrorf("this repo, branch and root path already have a connection")
		}
		return nil, fmt.Errorf("gitsync: create connection: %w", err)
	}
	for _, name := range adopt {
		var path string
		for _, it := range p.Items {
			if it.Name == name {
				path = it.Path
			}
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO git_bindings (skill_id, connection_id, path)
			SELECT id, $2, $3 FROM skills WHERE name = $1`, name, c.ID, path); err != nil {
			return nil, fmt.Errorf("gitsync: adopt %s: %w", name, err)
		}
	}
	if err := audit.Record(ctx, tx, u.Email, "git_connection.create", "git_connection:"+c.ID, map[string]any{
		"repo": c.Repo(), "branch": c.Branch, "root_path": c.RootPath, "prefix": c.Prefix,
		"service_account": saName, "adopted": adopt,
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

func findRule(rules []ownership.Rule, pattern string) *ownership.Rule {
	for i := range rules {
		if rules[i].Pattern == pattern {
			return &rules[i]
		}
	}
	return nil
}

// manageable loads a connection and checks the person may change it.
func (s *Syncer) manageable(ctx context.Context, u *auth.User, id string) (*Connection, error) {
	c, err := s.store.Connection(ctx, id)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, nil
	}
	if err := s.authorize(ctx, u, c.Prefix); err != nil {
		return nil, err
	}
	return c, nil
}

// Adopt binds a native skill to a connection after it was refused as taken.
func (s *Syncer) Adopt(ctx context.Context, u *auth.User, id, name string) (*Connection, error) {
	c, err := s.manageable(ctx, u, id)
	if err != nil || c == nil {
		return c, err
	}
	if !ownership.Matches(c.Prefix, name) {
		return nil, userErrorf("%s is outside the prefix %s", name, c.Prefix)
	}
	var path string
	err = s.pool.QueryRow(ctx, `SELECT path FROM git_connection_refusals WHERE connection_id = $1 AND skill_name = $2`,
		id, name).Scan(&path)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, userErrorf("%s is not a folder this connection refused as taken", name)
	}
	if err != nil {
		return nil, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `
		INSERT INTO git_bindings (skill_id, connection_id, path)
		SELECT id, $2, $3 FROM skills WHERE name = $1
		ON CONFLICT (skill_id) DO NOTHING`, name, id, path)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, userErrorf("%s does not exist or already belongs to a connection", name)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM git_connection_ignores WHERE connection_id = $1 AND skill_name = $2`, id, name); err != nil {
		return nil, err
	}
	if err := audit.Record(ctx, tx, u.Email, "git_connection.adopt", "skill:"+name, map[string]any{"connection": id}); err != nil {
		return nil, err
	}
	return c, tx.Commit(ctx)
}

// Detach makes a git-bound skill native and keeps the connection from
// adopting it again.
func (s *Syncer) Detach(ctx context.Context, u *auth.User, name string) error {
	b, err := s.store.BindingForSkill(ctx, name)
	if err != nil {
		return err
	}
	if b == nil {
		return userErrorf("%s is not git-bound", name)
	}
	if _, err := s.manageable(ctx, u, b.ConnectionID); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM git_bindings WHERE skill_id = $1`, b.SkillID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE skills SET removed_from_git_at = NULL, removed_from_git_commit = '' WHERE id = $1`, b.SkillID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO git_connection_ignores (connection_id, skill_name) VALUES ($1, $2)
		ON CONFLICT DO NOTHING`, b.ConnectionID, name); err != nil {
		return err
	}
	if err := audit.Record(ctx, tx, u.Email, "git_connection.detach", "skill:"+name, map[string]any{"connection": b.ConnectionID}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// SetContestMode changes when a connection's PRs get a contest.
func (s *Syncer) SetContestMode(ctx context.Context, u *auth.User, id, mode string) (*Connection, error) {
	if mode != ContestOff && mode != ContestOnRequest && mode != ContestReady {
		return nil, userErrorf("unknown contest setting %q", mode)
	}
	c, err := s.manageable(ctx, u, id)
	if err != nil || c == nil {
		return c, err
	}
	if _, err := s.pool.Exec(ctx, `UPDATE git_connections SET contest_mode = $2 WHERE id = $1`, id, mode); err != nil {
		return nil, err
	}
	return s.store.Connection(ctx, id)
}

// Delete removes a connection. Its skills become native and keep every
// version; its service account goes, and with it its ownership membership.
func (s *Syncer) Delete(ctx context.Context, u *auth.User, id string) (bool, error) {
	c, err := s.manageable(ctx, u, id)
	if err != nil || c == nil {
		return false, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `UPDATE skills SET removed_from_git_at = NULL, removed_from_git_commit = ''
		WHERE id IN (SELECT skill_id FROM git_bindings WHERE connection_id = $1)`, id); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM git_connections WHERE id = $1`, id); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM auth_users WHERE id = $1 AND kind = 'service'`, c.ServiceAccountID); err != nil {
		return false, err
	}
	if err := audit.Record(ctx, tx, u.Email, "git_connection.delete", "git_connection:"+id, map[string]any{"repo": c.Repo()}); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

// Repos lists the repositories the App can reach.
func (s *Syncer) Repos(ctx context.Context) ([]Repo, error) {
	gh, err := s.github(ctx)
	if err != nil {
		return nil, err
	}
	return gh.Repos(ctx)
}

// UserError is a request the person can correct. Routes answer it with 422.
type UserError struct{ msg string }

func (e *UserError) Error() string { return e.msg }

func userErrorf(format string, args ...any) error {
	return &UserError{msg: fmt.Sprintf(format, args...)}
}

func (d Deps) publicURL() string {
	if d.PublicURL == nil {
		return ""
	}
	return d.PublicURL()
}
