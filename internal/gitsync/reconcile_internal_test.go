package gitsync

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/skael-dev/skael/internal/auth"
	"github.com/skael-dev/skael/internal/contest"
	"github.com/skael-dev/skael/internal/evalqueue"
	"github.com/skael-dev/skael/internal/evalsuite"
	"github.com/skael-dev/skael/internal/ownership"
	"github.com/skael-dev/skael/internal/platform"
	"github.com/skael-dev/skael/internal/sets"
	"github.com/skael-dev/skael/internal/skill"
	"github.com/skael-dev/skael/internal/submissions"
	"github.com/skael-dev/skael/internal/testutil"
)

// fakeRepo serves the GitHub API for one repo whose branch head moves.
type fakeRepo struct {
	mu      sync.Mutex
	head    string
	commits map[string]map[string]string // sha -> path -> content
	blobs   map[string]string
	trees   map[string]map[string]string
	refs    map[string]string
	prState string // open, merged or closed
	prHead  string
	seq     int

	pulls     []map[string]any // open pull requests into main
	pullFiles map[int][]string
	checks    map[int64]map[string]any
}

func (f *fakeRepo) push(sha string, files map[string]string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commits[sha], f.head = files, sha
}

func (f *fakeRepo) tarball(sha string) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	root := "acme-skills-" + sha + "/"
	_ = tw.WriteHeader(&tar.Header{Name: root, Typeflag: tar.TypeDir, Mode: 0o755})
	for path, body := range f.commits[sha] {
		_ = tw.WriteHeader(&tar.Header{Name: root + path, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))})
		_, _ = tw.Write([]byte(body))
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func (f *fakeRepo) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		p := r.URL.Path
		switch {
		case p == "/app/installations/7/access_tokens":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"token":"inst-token","expires_at":"2999-01-01T00:00:00Z"}`))
		case p == "/repos/acme/skills/installation":
			_, _ = w.Write([]byte(`{"id":7}`))
		case p == "/repos/acme/skills/branches/main":
			_, _ = fmt.Fprintf(w, `{"commit":{"sha":%q}}`, f.head)
		case strings.HasPrefix(p, "/repos/acme/skills/commits/"):
			_, _ = w.Write([]byte(`{"author":{"login":"dev-one"}}`))
		case strings.HasPrefix(p, "/repos/acme/skills/tarball/"):
			_, _ = w.Write(f.tarball(strings.TrimPrefix(p, "/repos/acme/skills/tarball/")))
		case strings.HasPrefix(p, "/repos/acme/skills/git/commits/") && r.Method == http.MethodGet:
			_, _ = fmt.Fprintf(w, `{"tree":{"sha":"tree-%s"}}`, strings.TrimPrefix(p, "/repos/acme/skills/git/commits/"))
		case strings.HasPrefix(p, "/repos/acme/skills/git/trees/tree-"):
			var entries []map[string]string
			for path := range f.commits[strings.TrimPrefix(p, "/repos/acme/skills/git/trees/tree-")] {
				entries = append(entries, map[string]string{"path": path, "type": "blob"})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"tree": entries})
		case p == "/repos/acme/skills/git/blobs":
			var in struct{ Content string }
			_ = json.NewDecoder(r.Body).Decode(&in)
			raw, _ := base64.StdEncoding.DecodeString(in.Content)
			f.seq++
			id := fmt.Sprintf("blob-%d", f.seq)
			f.blobs[id] = string(raw)
			w.WriteHeader(http.StatusCreated)
			_, _ = fmt.Fprintf(w, `{"sha":%q}`, id)
		case p == "/repos/acme/skills/git/trees":
			var in struct {
				BaseTree string `json:"base_tree"`
				Tree     []struct {
					Path string  `json:"path"`
					SHA  *string `json:"sha"`
				} `json:"tree"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			files := map[string]string{}
			for k, v := range f.commits[strings.TrimPrefix(in.BaseTree, "tree-")] {
				files[k] = v
			}
			for _, e := range in.Tree {
				if e.SHA == nil {
					delete(files, e.Path)
				} else {
					files[e.Path] = f.blobs[*e.SHA]
				}
			}
			f.seq++
			id := fmt.Sprintf("newtree-%d", f.seq)
			f.trees[id] = files
			w.WriteHeader(http.StatusCreated)
			_, _ = fmt.Fprintf(w, `{"sha":%q}`, id)
		case p == "/repos/acme/skills/git/commits":
			var in struct{ Tree string }
			_ = json.NewDecoder(r.Body).Decode(&in)
			f.seq++
			id := fmt.Sprintf("prc%d", f.seq)
			f.commits[id] = f.trees[in.Tree]
			w.WriteHeader(http.StatusCreated)
			_, _ = fmt.Fprintf(w, `{"sha":%q}`, id)
		case p == "/repos/acme/skills/git/refs":
			var in struct{ Ref, SHA string }
			_ = json.NewDecoder(r.Body).Decode(&in)
			f.refs[in.Ref] = in.SHA
			f.prHead = in.SHA
			w.WriteHeader(http.StatusCreated)
		case strings.HasPrefix(p, "/repos/acme/skills/git/refs/heads/") && r.Method == http.MethodDelete:
			delete(f.refs, strings.TrimPrefix(p, "/repos/acme/skills/git/"))
			w.WriteHeader(http.StatusNoContent)
		case p == "/repos/acme/skills/pulls" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(f.pulls)
		case strings.HasPrefix(p, "/repos/acme/skills/pulls/") && strings.HasSuffix(p, "/files"):
			var n int
			_, _ = fmt.Sscanf(strings.TrimPrefix(p, "/repos/acme/skills/pulls/"), "%d", &n)
			var out []map[string]string
			for _, name := range f.pullFiles[n] {
				out = append(out, map[string]string{"filename": name})
			}
			_ = json.NewEncoder(w).Encode(out)
		case p == "/repos/acme/skills/check-runs":
			var in map[string]any
			_ = json.NewDecoder(r.Body).Decode(&in)
			f.seq++
			id := int64(f.seq)
			f.checks[id] = in
			w.WriteHeader(http.StatusCreated)
			_, _ = fmt.Fprintf(w, `{"id":%d}`, id)
		case strings.HasPrefix(p, "/repos/acme/skills/check-runs/") && r.Method == http.MethodPatch:
			var id int64
			_, _ = fmt.Sscanf(strings.TrimPrefix(p, "/repos/acme/skills/check-runs/"), "%d", &id)
			var in map[string]any
			_ = json.NewDecoder(r.Body).Decode(&in)
			for k, v := range in {
				f.checks[id][k] = v
			}
		case p == "/repos/acme/skills/pulls":
			f.prState = "open"
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"number":1,"html_url":"https://github.com/acme/skills/pull/1","state":"open"}`))
		case p == "/repos/acme/skills/pulls/1":
			state, merged := "open", false
			switch f.prState {
			case "merged":
				state, merged = "closed", true
			case "closed":
				state = "closed"
			}
			_, _ = fmt.Fprintf(w, `{"number":1,"state":%q,"merged":%v,"merge_commit_sha":%q}`, state, merged, f.prHead)
		default:
			http.NotFound(w, r)
		}
	})
}

func skillMD(name, body string) string {
	return "---\nname: " + name + "\ndescription: " + name + " does a thing\n---\n\n" + body + "\n"
}

type harness struct {
	admin  *auth.User
	pool   *pgxpool.Pool
	syncer *Syncer
	skills *skill.Store
	repo   *fakeRepo
	conn   *Connection
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	ctx := context.Background()
	pool := testutil.SetupTestDB(t)
	keys, err := platform.ParseKeyring("k1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	if err != nil {
		t.Fatal(err)
	}
	repo := &fakeRepo{commits: map[string]map[string]string{}, blobs: map[string]string{},
		trees: map[string]map[string]string{}, refs: map[string]string{},
		pullFiles: map[int][]string{}, checks: map[int64]map[string]any{}}
	gh := httptest.NewServer(repo.handler())
	t.Cleanup(gh.Close)

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	store := NewStore(pool, keys)
	if err := store.save(ctx, &App{
		AppID: 1, Slug: "skael", Name: "Skael", OwnerLogin: "acme", HTMLURL: "https://github.com/apps/skael",
		WebBaseURL: "https://github.com", APIBaseURL: gh.URL, ClientID: "c",
		PrivateKey:    pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}),
		WebhookSecret: []byte("ws"), ClientSecret: []byte("cs"), CreatedBy: "admin@example.com",
	}); err != nil {
		t.Fatal(err)
	}

	users := auth.NewUserStore(pool)
	skills := skill.NewStore(pool)
	storage, err := platform.NewLocalStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	syncer := NewSyncer(store, gh.Client())
	owners := ownership.NewResolver(ownership.NewStore(pool), users)
	syncer.Bind(Deps{
		Skills:      skills,
		Publisher:   skill.NewPublisher(skills, storage, skill.RouteOptions{Ownership: owners}),
		Submissions: submissions.NewStore(pool),
		Contests:    contest.NewStore(pool),
		Queue:       evalqueue.NewPool(pool),
		Suites:      evalsuite.NewRegistry(pool, storage),
		Storage:     storage,
		Owners:      owners,
		Users:       users,
		PublicURL:   func() string { return "https://skael.example.com" },
	})

	var adminID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO auth_users (id, email, email_normalized, display_name, role, kind, created_at)
		VALUES (gen_random_uuid(), 'admin@example.com', 'admin@example.com', 'Admin', 'admin', 'human', now())
		RETURNING id`).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	admin := &auth.User{ID: adminID, Email: "admin@example.com", Role: auth.RoleAdmin, Kind: auth.KindHuman}

	repo.push("c1", map[string]string{
		"skills/deploy/SKILL.md":  skillMD("platform:deploy", "Deploy it."),
		"skills/release/SKILL.md": skillMD("platform:release", "Release it."),
		"skills/stray/SKILL.md":   skillMD("payments:stray", "Not ours."),
		"skills/dupe-a/SKILL.md":  skillMD("platform:dupe", "One."),
		"skills/dupe-b/SKILL.md":  skillMD("platform:dupe", "Two."),
	})
	conn, err := syncer.Create(ctx, admin, CreateRequest{
		RepoOwner: "acme", RepoName: "skills", Branch: "main", RootPath: "skills", Prefix: "platform:*",
	}, nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return &harness{admin: admin, pool: pool, syncer: syncer, skills: skills, repo: repo, conn: conn}
}

func (h *harness) sync(t *testing.T) {
	t.Helper()
	if err := h.syncer.Reconcile(context.Background(), h.conn.ID, false); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
}

func (h *harness) latest(t *testing.T, name string) (int, string) {
	t.Helper()
	sk, err := h.skills.GetByName(context.Background(), name)
	if err != nil || sk == nil {
		t.Fatalf("skill %s: %v", name, err)
	}
	v, err := h.skills.GetVersion(context.Background(), name, sk.LatestVersion)
	if err != nil || v == nil {
		t.Fatalf("version of %s: %v", name, err)
	}
	return sk.LatestVersion, v.PublishedBy
}

// One sync publishes every accepted folder as the connection's service
// account with no ownership hold, and refuses the rest with a reason.
func TestReconcile_PublishesAcceptedFoldersAndRefusesTheRest(t *testing.T) {
	h := newHarness(t)
	h.sync(t)

	for _, name := range []string{"platform:deploy", "platform:release"} {
		v, by := h.latest(t, name)
		if v != 1 || !strings.HasSuffix(by, "@service-account.invalid") {
			t.Errorf("%s: version %d by %q, want v1 released by the service account", name, v, by)
		}
	}
	refusals, err := h.syncer.store.Refusals(context.Background(), h.conn.ID)
	if err != nil {
		t.Fatal(err)
	}
	reasons := map[string]string{}
	for _, r := range refusals {
		reasons[r.Path] = r.Reason
	}
	if !strings.Contains(reasons["skills/stray"], "outside the prefix") {
		t.Errorf("stray: %q", reasons["skills/stray"])
	}
	if !strings.Contains(reasons["skills/dupe-a"], "another folder") || !strings.Contains(reasons["skills/dupe-b"], "another folder") {
		t.Errorf("duplicate names were not both refused: %v", reasons)
	}
	if sk, _ := h.skills.GetByName(context.Background(), "payments:stray"); sk != nil {
		t.Error("a folder outside the prefix created a skill")
	}
}

// A changed folder publishes the next version and an unchanged one does not.
// A rename keeps the history and lets an exact pin follow; a deleted folder
// keeps its skill served and marked.
func TestReconcile_UpdatesRenamesAndMarksRemoval(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.sync(t)

	h.repo.push("c2", map[string]string{
		"skills/deploy/SKILL.md":  skillMD("platform:ship", "Deploy it."),
		"skills/release/SKILL.md": skillMD("platform:release", "Release it, carefully."),
	})
	h.sync(t)

	if v, _ := h.latest(t, "platform:release"); v != 2 {
		t.Errorf("platform:release is at v%d, want v2 after its folder changed", v)
	}
	// The frontmatter name changed, so the archive did: v2 on top of the kept v1.
	if v, _ := h.latest(t, "platform:ship"); v != 2 {
		t.Errorf("the renamed skill is at v%d, want v2 on top of its history", v)
	}
	if v1, err := h.skills.GetVersion(ctx, "platform:ship", 1); err != nil || v1 == nil {
		t.Errorf("the renamed skill lost its v1: %v", err)
	}
	var reason, canonical string
	if err := h.pool.QueryRow(ctx, `SELECT reason, canonical FROM skill_aliases WHERE alias = 'platform:deploy'`).
		Scan(&reason, &canonical); err != nil {
		t.Fatalf("no alias for the old name: %v", err)
	}
	if reason != "rename" || canonical != "platform:ship" {
		t.Errorf("alias = %s -> %s, want a rename to platform:ship", reason, canonical)
	}

	// A set pinned to the old name's exact version follows the rename.
	res, err := sets.Resolve(ctx, h.pool, &sets.Set{Name: "pins", Entries: []sets.SetEntry{{Name: "platform:deploy", Version: 1}}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(res.Entries) != 1 || res.Entries[0].Name != "platform:ship" || res.Entries[0].Version != 1 {
		t.Errorf("platform:deploy@1 resolved to %+v (excluded %+v), want platform:ship@1", res.Entries, res.Excluded)
	}

	h.repo.push("c3", map[string]string{
		"skills/deploy/SKILL.md": skillMD("platform:ship", "Deploy it."),
	})
	h.sync(t)
	sk, err := h.skills.GetByName(ctx, "platform:release")
	if err != nil || sk == nil || sk.LatestVersion != 2 {
		t.Fatalf("a removed folder took its skill with it: %+v %v", sk, err)
	}
	var removed bool
	if err := h.pool.QueryRow(ctx, `SELECT removed_from_git_at IS NOT NULL FROM skills WHERE name = 'platform:release'`).
		Scan(&removed); err != nil || !removed {
		t.Errorf("platform:release is not marked removed from git (%v)", err)
	}
	res, err = sets.Resolve(ctx, h.pool, &sets.Set{Name: "latest", Entries: []sets.SetEntry{{Name: "platform:release"}}})
	if err != nil || len(res.Entries) != 1 || !res.Entries[0].RemovedFromGit {
		t.Errorf("a set naming a removed skill resolves without the warning: %+v %v", res, err)
	}
}

// A connection that loses its branch is errored and changes no skill.
func TestReconcile_AnUnreachableBranchChangesNothing(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.sync(t)
	if _, err := h.pool.Exec(ctx, `UPDATE git_connections SET branch = 'gone' WHERE id = $1`, h.conn.ID); err != nil {
		t.Fatal(err)
	}
	h.sync(t)
	c, err := h.syncer.store.Connection(ctx, h.conn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if c.Status != "error" || !strings.Contains(c.LastError, "gone") {
		t.Errorf("status %s, error %q, want an errored connection naming the branch", c.Status, c.LastError)
	}
	if v, _ := h.latest(t, "platform:deploy"); v != 1 {
		t.Errorf("an unreachable branch changed platform:deploy to v%d", v)
	}
}

// The service account joins the prefix rule, so a sync publishes without an
// ownership hold, and the person connecting owns a prefix nobody owned.
func TestCreate_GivesTheConnectionThePrefix(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	rule, err := ownership.NewStore(h.pool).GetByPattern(ctx, "platform:*")
	if err != nil || rule == nil {
		t.Fatalf("no rule for the prefix: %v", err)
	}
	members := strings.Join(rule.Members, ",")
	if !strings.Contains(members, h.conn.ServiceAccountID) || len(rule.Members) != 2 {
		t.Errorf("rule members = %v, want the creator and the service account", rule.Members)
	}
	raw, _ := json.Marshal(h.conn)
	if strings.Contains(string(raw), "inst-token") {
		t.Error("a token reached the connection record")
	}
}

func packed(t *testing.T, files map[string]string) []byte {
	t.Helper()
	dir := t.TempDir()
	for p, body := range files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, p), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	raw, _, _, err := skill.Pack(dir)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// A publish to a git-bound skill becomes a pull request that replaces the
// folder. Its merge publishes the version and approves the waiting submission.
func TestChange_MergePublishesAndApprovesTheSubmission(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.sync(t)

	asAdmin := auth.ContextWithUser(ctx, h.admin)
	change, err := h.syncer.OpenChange(asAdmin, "platform:release", packed(t, map[string]string{
		"SKILL.md": skillMD("platform:release", "Release it, with notes."),
	}), "From a submission")
	if err != nil {
		t.Fatalf("OpenChange: %v", err)
	}
	h.repo.mu.Lock()
	files := h.repo.commits[h.repo.prHead]
	h.repo.mu.Unlock()
	if !strings.Contains(files["skills/release/SKILL.md"], "with notes") {
		t.Fatalf("the PR commit does not carry the archive: %v", files)
	}
	var subID string
	if err := h.pool.QueryRow(ctx, `
		INSERT INTO submissions (skill_name, consumer, submitted_by, rationale, archive_path, checksum, file_manifest, scan_result)
		VALUES ('platform:release', 'agent', 'agent@example.com', 'better', 'a', 'c', '[]', '{}') RETURNING id`).Scan(&subID); err != nil {
		t.Fatal(err)
	}
	if _, err := submissions.NewStore(h.pool).AttachChange(ctx, subID, change.ID, h.admin.Email, ""); err != nil {
		t.Fatalf("AttachChange: %v", err)
	}

	h.repo.mu.Lock()
	h.repo.prState = "merged"
	h.repo.mu.Unlock()
	h.repo.push("merge1", files)
	h.sync(t)

	if v, _ := h.latest(t, "platform:release"); v != 2 {
		t.Errorf("platform:release is at v%d after the merge, want v2", v)
	}
	var state string
	var version *int
	if err := h.pool.QueryRow(ctx, `SELECT state, version FROM git_changes WHERE id = $1`, change.ID).Scan(&state, &version); err != nil {
		t.Fatal(err)
	}
	if state != "merged" || version == nil || *version != 2 {
		t.Errorf("change is %s at %v, want merged at v2", state, version)
	}
	sub, err := submissions.NewStore(h.pool).Get(ctx, subID)
	if err != nil || sub.State != "approved" || sub.Version == nil || *sub.Version != 2 || sub.DecidedBy != h.admin.Email {
		t.Errorf("submission = %+v, %v; want approved at v2 by the approver", sub, err)
	}
}

// A closed pull request rejects its submission in the connection's name and
// publishes nothing.
func TestChange_CloseRejectsTheSubmission(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.sync(t)

	change, err := h.syncer.OpenChange(auth.ContextWithUser(ctx, h.admin), "platform:release",
		packed(t, map[string]string{"SKILL.md": skillMD("platform:release", "Nope.")}), "")
	if err != nil {
		t.Fatalf("OpenChange: %v", err)
	}
	var subID string
	if err := h.pool.QueryRow(ctx, `
		INSERT INTO submissions (skill_name, consumer, submitted_by, rationale, archive_path, checksum, file_manifest, scan_result, change_id)
		VALUES ('platform:release', 'agent', 'agent@example.com', 'r', 'a', 'c', '[]', '{}', $1) RETURNING id`, change.ID).Scan(&subID); err != nil {
		t.Fatal(err)
	}
	h.repo.mu.Lock()
	h.repo.prState = "closed"
	h.repo.mu.Unlock()
	h.sync(t)

	sub, err := submissions.NewStore(h.pool).Get(ctx, subID)
	if err != nil || sub.State != "rejected" || sub.Reason != "PR closed unmerged" || !strings.HasSuffix(sub.DecidedBy, "@service-account.invalid") {
		t.Errorf("submission = %+v, %v; want rejected by the connection", sub, err)
	}
	if v, _ := h.latest(t, "platform:release"); v != 1 {
		t.Errorf("a closed PR published v%d", v)
	}
}

// openPull adds an open pull request whose head changes files.
func (f *fakeRepo) openPull(n int, sha string, files map[string]string, changed []string, fork bool, labels ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commits[sha] = files
	head := map[string]any{"sha": sha, "repo": map[string]string{"full_name": "acme/skills"}}
	if fork {
		head["repo"] = map[string]string{"full_name": "stranger/skills"}
	}
	var ls []map[string]string
	for _, l := range labels {
		ls = append(ls, map[string]string{"name": l})
	}
	f.pulls = append(f.pulls, map[string]any{
		"number": n, "html_url": fmt.Sprintf("https://github.com/acme/skills/pull/%d", n), "title": "change",
		"head": head, "base": map[string]any{"repo": map[string]string{"full_name": "acme/skills"}}, "labels": ls,
	})
	f.pullFiles[n] = changed
}

func (f *fakeRepo) check(t *testing.T) map[string]any {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.checks) != 1 {
		t.Fatalf("%d check runs, want 1", len(f.checks))
	}
	for _, c := range f.checks {
		return c
	}
	return nil
}

// A pull request that changes a skill folder gets one Skael check with the
// scan's result. A contest waits for a request under the default setting, and
// the label starts one between the released version and the PR head.
func TestPulls_CheckAndContestOnRequest(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.sync(t)

	files := map[string]string{
		"skills/deploy/SKILL.md":  skillMD("platform:deploy", "Deploy it."),
		"skills/release/SKILL.md": skillMD("platform:release", "Release it, with a changelog."),
	}
	h.repo.openPull(5, "pr5a", files, []string{"skills/release/SKILL.md"}, false)
	h.sync(t)

	run := h.repo.check(t)
	if run["conclusion"] != "success" || run["head_sha"] != "pr5a" {
		t.Errorf("check = %v, want success on pr5a", run)
	}
	if actions, _ := run["actions"].([]any); len(actions) != 1 {
		t.Errorf("the check has no Run contest button: %v", run["actions"])
	}
	var contests int
	if err := h.pool.QueryRow(ctx, `SELECT count(*) FROM contests`).Scan(&contests); err != nil || contests != 0 {
		t.Fatalf("%d contests before any request, want 0 (%v)", contests, err)
	}

	h.repo.mu.Lock()
	h.repo.pulls[0]["labels"] = []map[string]string{{"name": ContestLabel}}
	h.repo.mu.Unlock()
	h.sync(t)

	var labels []string
	rows, err := h.pool.Query(ctx, `SELECT label FROM contest_candidates ORDER BY position`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var l string
		_ = rows.Scan(&l)
		labels = append(labels, l)
	}
	rows.Close()
	if strings.Join(labels, ",") != "platform:release@1,platform:release@pr5" {
		t.Errorf("contest candidates = %v, want the released version and the PR head", labels)
	}
	summary, _ := h.repo.check(t)["output"].(map[string]any)["summary"].(string)
	if !strings.Contains(summary, "contest running") {
		t.Errorf("the check does not report the running contest: %q", summary)
	}
}

// A fork never gets a contest without a request, even when every PR should.
func TestPulls_AForkGetsNoAutomaticContest(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	if _, err := h.pool.Exec(ctx, `UPDATE git_connections SET contest_mode = 'ready' WHERE id = $1`, h.conn.ID); err != nil {
		t.Fatal(err)
	}
	h.sync(t)
	h.repo.openPull(6, "fork6", map[string]string{
		"skills/release/SKILL.md": skillMD("platform:release", "Release it, says a stranger."),
	}, []string{"skills/release/SKILL.md"}, true)
	h.sync(t)

	var contests int
	if err := h.pool.QueryRow(ctx, `SELECT count(*) FROM contests`).Scan(&contests); err != nil || contests != 0 {
		t.Errorf("a fork started %d contests without a request (%v)", contests, err)
	}
	if _, err := h.syncer.store.RequestContest(ctx, h.conn.ID, 6, ""); err != nil {
		t.Fatal(err)
	}
	h.sync(t)
	if err := h.pool.QueryRow(ctx, `SELECT count(*) FROM contests`).Scan(&contests); err != nil || contests != 1 {
		t.Errorf("a requested contest on a fork: %d contests (%v), want 1", contests, err)
	}
}

// When the merged folder is the PR head a contest evaluated, the new version
// links that contest.
func TestPulls_MergedHeadLinksItsContest(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.sync(t)
	files := map[string]string{
		"skills/deploy/SKILL.md":  skillMD("platform:deploy", "Deploy it."),
		"skills/release/SKILL.md": skillMD("platform:release", "Release it, better."),
	}
	h.repo.openPull(7, "pr7", files, []string{"skills/release/SKILL.md"}, false, ContestLabel)
	h.sync(t)

	h.repo.mu.Lock()
	h.repo.pulls = nil
	h.repo.mu.Unlock()
	h.repo.push("merge7", files)
	h.sync(t)

	var linked *string
	if err := h.pool.QueryRow(ctx, `
		SELECT v.contest_id::text FROM skill_versions v JOIN skills s ON s.id = v.skill_id
		WHERE s.name = 'platform:release' AND v.version = 2`).Scan(&linked); err != nil {
		t.Fatal(err)
	}
	if linked == nil {
		t.Error("the merged version does not link the contest that evaluated its content")
	}
	var heads int
	if err := h.pool.QueryRow(ctx, `SELECT count(*) FROM git_pr_heads WHERE pr_number = 7`).Scan(&heads); err != nil || heads != 1 {
		t.Errorf("%d PR heads kept for the closed PR (%v), want the one a contest references", heads, err)
	}
}

// A pull request that adds a hardcoded secret fails its check, and offers no
// contest for a head the merge would refuse.
func TestPulls_ASecretFailsTheCheck(t *testing.T) {
	h := newHarness(t)
	h.sync(t)
	h.repo.openPull(8, "pr8", map[string]string{
		"skills/release/SKILL.md": skillMD("platform:release", "Configure the client with AKIAIOSFODNN7EXAMPLE before running."),
	}, []string{"skills/release/SKILL.md"}, false)
	h.sync(t)

	run := h.repo.check(t)
	if run["conclusion"] != "failure" {
		t.Errorf("conclusion = %v, want failure", run["conclusion"])
	}
	if _, ok := run["actions"]; ok {
		t.Error("a blocked head offers a contest")
	}
}

// A pull request open when the connection is created is checked before its
// skills exist. A later request still gets its contest.
func TestPulls_AHeadCheckedBeforeItsSkillExistedStillGetsAContest(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.repo.mu.Lock()
	base := h.repo.commits["c1"]
	h.repo.mu.Unlock()
	files := map[string]string{}
	for k, v := range base {
		files[k] = v
	}
	files["skills/release/SKILL.md"] = skillMD("platform:release", "Release it, later.")
	h.repo.openPull(9, "pr9", files, []string{"skills/release/SKILL.md"}, false)
	h.sync(t)

	if _, err := h.syncer.store.RequestContest(ctx, h.conn.ID, 9, ""); err != nil {
		t.Fatal(err)
	}
	h.sync(t)
	var contests int
	if err := h.pool.QueryRow(ctx, `SELECT count(*) FROM contests`).Scan(&contests); err != nil || contests != 1 {
		t.Errorf("%d contests (%v), want 1", contests, err)
	}
}
