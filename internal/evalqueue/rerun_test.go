package evalqueue_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/skael-dev/skael/internal/auth"
	"github.com/skael-dev/skael/internal/eval/suite"
	"github.com/skael-dev/skael/internal/evalqueue"
	"github.com/skael-dev/skael/internal/evalsuite"
	"github.com/skael-dev/skael/internal/platform"
	"github.com/skael-dev/skael/internal/quality"
	"github.com/skael-dev/skael/internal/skill"
	"github.com/skael-dev/skael/internal/testutil"
)

// rerunTestServer wires the full production route set needed to exercise the
// re-run endpoint end to end: skill routes (so publish can enqueue), the
// suite registry (so a suite can be registered and looked up), and the eval
// job queue itself.
type rerunTestServer struct {
	handler http.Handler
	skills  *skill.Store
	suites  *evalsuite.Registry
	queue   *evalqueue.PoolExecutor
	pool    *pgxpool.Pool
}

// rerunQueueAdapter and rerunSuiteAdapter mirror internal/server's
// evalQueueAdapter/evalSuiteAdapter: internal/skill cannot import
// internal/evalqueue or internal/evalsuite directly (both import
// internal/skill for route wiring), so the caller bridges the two.
type rerunQueueAdapter struct{ q *evalqueue.PoolExecutor }

func (a rerunQueueAdapter) Submit(ctx context.Context, j skill.EvalJobRequest) (string, error) {
	id, err := a.q.Submit(ctx, evalqueue.Job{
		SkillID:     j.SkillID,
		SkillName:   j.SkillName,
		Version:     j.Version,
		SuiteRef:    j.SuiteRef,
		Tier:        j.Tier,
		RequestedBy: j.RequestedBy,
	})
	return string(id), err
}

type rerunSuiteAdapter struct{ r *evalsuite.Registry }

func (a rerunSuiteAdapter) LatestForSkill(ctx context.Context, name string) (*skill.SuiteRecord, error) {
	rec, err := a.r.LatestForSkill(ctx, name)
	if err != nil {
		if errors.Is(err, evalsuite.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &skill.SuiteRecord{Ref: rec.Ref}, nil
}

func newRerunTestServerWithRole(t *testing.T, role string) *rerunTestServer {
	t.Helper()

	pool := testutil.SetupTestDB(t)
	skillStore := skill.NewStore(pool)
	q := evalqueue.NewPool(pool)
	qual := quality.NewStore(pool)

	storage, err := platform.NewLocalStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStorage: %v", err)
	}
	suiteStorage, err := platform.NewLocalStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStorage: %v", err)
	}
	suiteRegistry := evalsuite.NewRegistry(pool, suiteStorage)

	user := &auth.User{
		ID:    "00000000-0000-0000-0000-0000000000bb",
		Email: "user@example.com",
		Role:  role,
	}

	r := chi.NewMux()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			req = req.WithContext(auth.ContextWithUser(req.Context(), user))
			next.ServeHTTP(w, req)
		})
	})
	api := humachi.New(r, huma.DefaultConfig("Test API", "1.0.0"))
	skill.RegisterRoutes(api, r, skillStore, storage, skill.RouteOptions{
		Queue:  rerunQueueAdapter{q: q},
		Suites: rerunSuiteAdapter{r: suiteRegistry},
	})
	evalsuite.RegisterRoutes(api, r, suiteRegistry, skillStore, evalsuite.RouteOptions{Claims: q})
	evalqueue.RegisterRoutes(api, q, qual, skillStore, suiteRegistry, evalqueue.RouteOptions{
		Releaser: skill.NewReleaser(skillStore),
	})

	return &rerunTestServer{handler: r, skills: skillStore, suites: suiteRegistry, queue: q, pool: pool}
}

func newRerunTestServerAsAdmin(t *testing.T) *rerunTestServer {
	t.Helper()
	return newRerunTestServerWithRole(t, auth.RoleAdmin)
}

func newRerunTestServerAsMember(t *testing.T) *rerunTestServer {
	t.Helper()
	return newRerunTestServerWithRole(t, auth.RoleMember)
}

func (s *rerunTestServer) createSkill(t *testing.T, name string) string {
	t.Helper()
	sk, err := s.skills.Create(context.Background(), name, name, "test skill", "# "+name, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("createSkill(%s): %v", name, err)
	}
	return sk.ID
}

// registerSuite uploads a minimal fixture suite for name via a real HTTP
// call, so the re-run endpoint's suite lookup finds it exactly as it would
// in production. Returns the suite's ref.
func (s *rerunTestServer) registerSuite(t *testing.T, name string) string {
	t.Helper()
	return s.registerSuiteForJob(t, name, "", "")
}

// registerSuiteForJob is registerSuite with an optional job claim attached —
// the derive path's push, which the server attributes to the job in flight.
func (s *rerunTestServer) registerSuiteForJob(t *testing.T, name, jobID, token string) string {
	t.Helper()

	dir := t.TempDir()
	// The prompt embeds the skill name so two skills' fixture sets hash to
	// distinct content-addressed refs — a ref is content-addressed
	// independent of the skill field, so two identical fixtures would
	// otherwise collide onto one ref.
	sfx := &suite.EvalSet{
		SkillName: name,
		Evals: []suite.Eval{
			{ID: 1, Prompt: "Do the thing for " + name + ".", Expectations: []string{"it did the thing"}},
		},
	}
	if err := suite.WriteEvalSet(dir, sfx); err != nil {
		t.Fatalf("write eval set fixture: %v", err)
	}
	archive, err := evalsuite.PackDir(dir)
	if err != nil {
		t.Fatalf("PackDir: %v", err)
	}

	body, err := json.Marshal(struct {
		Skill       string `json:"skill"`
		SpecVersion int    `json:"spec_version"`
		Checks      []struct {
			TaskID string `json:"task_id"`
			OK     bool   `json:"ok"`
		} `json:"checks"`
		ArchiveBase64 string `json:"archive_base64"`
		JobID         string `json:"job_id,omitempty"`
		ClaimToken    string `json:"claim_token,omitempty"`
	}{
		Skill:       name,
		SpecVersion: 1,
		Checks: []struct {
			TaskID string `json:"task_id"`
			OK     bool   `json:"ok"`
		}{{TaskID: "t1", OK: true}},
		ArchiveBase64: base64.StdEncoding.EncodeToString(archive),
		JobID:         jobID,
		ClaimToken:    token,
	})
	if err != nil {
		t.Fatalf("marshal suite body: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/eval/suites", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	s.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("registerSuite(%s): %d: %s", name, rr.Code, rr.Body)
	}

	var out struct {
		Ref string `json:"ref"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("registerSuite(%s): unmarshal: %v", name, err)
	}
	return out.Ref
}

func (s *rerunTestServer) publish(t *testing.T, name string, archive []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/skills/"+name+"/versions", strings.NewReader(string(archive)))
	req.Header.Set("Content-Type", "application/gzip")
	rr := httptest.NewRecorder()
	s.handler.ServeHTTP(rr, req)
	return rr
}

func (s *rerunTestServer) postJSON(t *testing.T, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("postJSON: marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	s.handler.ServeHTTP(rr, req)
	return rr
}

// drainQueue claims and completes every job currently queued, so a
// publish-triggered enqueue does not leave a job sitting around to confuse an
// assertion made about a later, unrelated job.
func (s *rerunTestServer) drainQueue(t *testing.T) {
	t.Helper()
	for {
		j, token, ok, err := s.queue.Claim(context.Background(), "drain-worker", 60_000_000_000)
		if err != nil {
			t.Fatalf("drainQueue: claim: %v", err)
		}
		if !ok {
			return
		}
		_ = token
		if err := s.queue.Complete(context.Background(), j.ID, "drain-worker"); err != nil {
			t.Fatalf("drainQueue: complete: %v", err)
		}
	}
}

// fixtureBundle packs a minimal, valid skill archive.
func fixtureBundle(t *testing.T) []byte {
	t.Helper()
	dir := t.TempDir()
	skillMD := strings.Join([]string{
		"---",
		"name: deploy-helper",
		"description: deploys things",
		"---",
		"# deploy-helper",
		"This is the skill body.",
	}, "\n")
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(skillMD), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	archive, _, _, err := skill.Pack(dir)
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}
	return archive
}

// newRerunEnv, publishSkill, post, latestJob, pushSuite and
// createSkillWithoutPublishing below are thin aliases over the fixtures
// above, named to match the task brief's test bodies without duplicating the
// server wiring they already do.

func newRerunEnv(t *testing.T) *rerunTestServer {
	t.Helper()
	return newRerunTestServerAsAdmin(t)
}

// publishSkill creates and publishes a minimal skill, then drains the
// publish-triggered job so it isn't mistaken for the job under test.
func (s *rerunTestServer) publishSkill(t *testing.T, name string) {
	t.Helper()
	s.createSkill(t, name)
	rr := s.publish(t, name, fixtureBundle(t))
	if rr.Code != http.StatusCreated {
		t.Fatalf("publishSkill(%s): %d: %s", name, rr.Code, rr.Body)
	}
	s.drainQueue(t)
}

func (s *rerunTestServer) pushSuite(t *testing.T, name string) string {
	t.Helper()
	return s.registerSuite(t, name)
}

func (s *rerunTestServer) createSkillWithoutPublishing(t *testing.T, name string) {
	t.Helper()
	s.createSkill(t, name)
}

// post issues a raw-body JSON POST, for tests asserting on a specific
// request body rather than one built from a Go value.
func (s *rerunTestServer) post(t *testing.T, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	s.handler.ServeHTTP(rr, req)
	return rr
}

// latestJob returns the most recently created eval job for a skill.
func (s *rerunTestServer) latestJob(t *testing.T, name string) evalqueue.Job {
	t.Helper()
	sk, err := s.skills.GetByName(context.Background(), name)
	if err != nil || sk == nil {
		t.Fatalf("latestJob(%s): GetByName: sk=%v err=%v", name, sk, err)
	}
	jobs, err := s.queue.ListBySkill(context.Background(), sk.ID)
	if err != nil {
		t.Fatalf("latestJob(%s): ListBySkill: %v", name, err)
	}
	if len(jobs) == 0 {
		t.Fatalf("latestJob(%s): no jobs found", name)
	}
	return jobs[0]
}

func TestRerunEval_EnqueuesWithAnEmptyRefWhenNoSuiteExists(t *testing.T) {
	// An imported skill has no suite. Refusing here is what made evaluation
	// unreachable for every skill not authored through whetstone.
	env := newRerunEnv(t)
	env.publishSkill(t, "imported-skill")

	res := env.post(t, "/api/skills/imported-skill/evals", `{}`)
	if res.Code != http.StatusAccepted {
		t.Fatalf("status %d, want 202: %s", res.Code, res.Body)
	}

	job := env.latestJob(t, "imported-skill")
	if job.SuiteRef != "" {
		t.Fatalf("job suite_ref = %q, want empty so the worker derives one", job.SuiteRef)
	}
}

func TestRerunEval_UsesTheStoredSuiteWhenOneExists(t *testing.T) {
	// Derivation is paid for once: the second run finds the suite the first
	// one pushed, which is also what keeps the two scores comparable.
	env := newRerunEnv(t)
	env.publishSkill(t, "authored-skill")
	ref := env.pushSuite(t, "authored-skill")

	env.post(t, "/api/skills/authored-skill/evals", `{}`)

	job := env.latestJob(t, "authored-skill")
	if job.SuiteRef != ref {
		t.Fatalf("job suite_ref = %q, want the stored suite %q", job.SuiteRef, ref)
	}
}

func TestRerunEval_StillRejectsAnUnknownExplicitRef(t *testing.T) {
	env := newRerunEnv(t)
	env.publishSkill(t, "demo")

	res := env.post(t, "/api/skills/demo/evals", `{"suite_ref":"no-such-ref"}`)
	if res.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404 for a named ref that does not exist", res.Code)
	}
}

func TestRerunEval_StillRejectsAnUnpublishedSkill(t *testing.T) {
	// A skill with no released version has nothing to evaluate; deriving a
	// suite for it would not change that.
	env := newRerunEnv(t)
	env.createSkillWithoutPublishing(t, "empty")

	res := env.post(t, "/api/skills/empty/evals", `{}`)
	if res.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404 for a skill with no published version", res.Code)
	}
}

func TestRerun_UsesTheStoredSuiteAndTheRequestedPanel(t *testing.T) {
	srv := newRerunTestServerAsAdmin(t)
	skillID := srv.createSkill(t, "deploy-helper")
	ref := srv.registerSuite(t, "deploy-helper")
	srv.publish(t, "deploy-helper", fixtureBundle(t)) // version 1, enqueues job A
	srv.drainQueue(t)

	resp := srv.postJSON(t, "/api/skills/deploy-helper/evals", map[string]any{
		"models": []string{"opus-next"},
	})
	if resp.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", resp.Code, resp.Body)
	}
	var out struct {
		JobID string `json:"job_id"`
	}
	_ = json.Unmarshal(resp.Body.Bytes(), &out)

	job, err := srv.queue.Get(context.Background(), evalqueue.JobID(out.JobID))
	if err != nil || job == nil {
		t.Fatalf("Get(%s): job=%v err=%v", out.JobID, job, err)
	}
	if job.SuiteRef != ref {
		t.Fatalf("re-run used suite %s, want the stored %s — a new suite is a different measurement", job.SuiteRef, ref)
	}
	if len(job.Panel.Models) != 1 || job.Panel.Models[0] != "opus-next" {
		t.Fatalf("panel = %+v, want the requested one", job.Panel)
	}
	if job.SkillID != skillID || job.Version != 1 {
		t.Fatalf("job targets %s v%d, want %s v1", job.SkillID, job.Version, skillID)
	}
}

// Superseded by TestRerunEval_EnqueuesWithAnEmptyRefWhenNoSuiteExists: a
// missing suite used to 404 here, which made evaluation unreachable for
// every skill not authored through whetstone. It now enqueues with an empty
// suite_ref instead.
func TestRerun_202WhenNoSuiteIsRegistered(t *testing.T) {
	srv := newRerunTestServerAsAdmin(t)
	srv.createSkill(t, "nosuite")
	srv.publish(t, "nosuite", fixtureBundle(t))
	srv.drainQueue(t)
	resp := srv.postJSON(t, "/api/skills/nosuite/evals", map[string]any{})
	if resp.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", resp.Code, resp.Body)
	}
}

func TestRerun_RequiresPrivilege(t *testing.T) {
	srv := newRerunTestServerAsMember(t)
	srv.createSkill(t, "deploy-helper")
	srv.registerSuite(t, "deploy-helper")
	resp := srv.postJSON(t, "/api/skills/deploy-helper/evals", map[string]any{})
	if resp.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.Code)
	}
}

// A caller-supplied suite_ref that belongs to a different skill must be
// rejected: eval_suites.ref is globally unique and eval_jobs.suite_ref has no
// foreign key, so an unchecked ref would let a privileged caller run another
// skill's tasks (and verifiers written for a different tool) and have the
// score attributed to this skill — the same comparability hazard the default
// path exists to prevent, arriving through the explicit suite_ref door.
func TestRerun_RejectsASuiteRefFromAnotherSkill(t *testing.T) {
	srv := newRerunTestServerAsAdmin(t)
	srv.createSkill(t, "deploy-helper")
	srv.publish(t, "deploy-helper", fixtureBundle(t))
	srv.registerSuite(t, "deploy-helper")

	srv.createSkill(t, "other-skill")
	otherRef := srv.registerSuite(t, "other-skill")
	srv.drainQueue(t)

	resp := srv.postJSON(t, "/api/skills/deploy-helper/evals", map[string]any{
		"suite_ref": otherRef,
	})
	if resp.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", resp.Code, resp.Body)
	}
	if !strings.Contains(resp.Body.String(), otherRef) || !strings.Contains(resp.Body.String(), "other-skill") {
		t.Fatalf("the error does not name the ref and the skill it actually belongs to: %s", resp.Body)
	}
}

// A caller-supplied suite_ref that does not exist at all must fail at
// submission time (404), not silently enqueue a job that only fails later
// when a worker tries to fetch it.
func TestRerun_RejectsANonexistentSuiteRef(t *testing.T) {
	srv := newRerunTestServerAsAdmin(t)
	srv.createSkill(t, "deploy-helper")
	srv.publish(t, "deploy-helper", fixtureBundle(t))

	resp := srv.postJSON(t, "/api/skills/deploy-helper/evals", map[string]any{
		"suite_ref": "sha256:does-not-exist",
	})
	if resp.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", resp.Code, resp.Body)
	}
}

// A skill created but never published has LatestVersion == 0. Enqueuing a
// job against version 0 defers a certain failure to whatever a worker makes
// of it; refuse at request time instead, the same way the sibling call sites
// in internal/skill/routes.go (:384, :883) do.
func TestRerun_404WhenSkillHasNoPublishedVersion(t *testing.T) {
	srv := newRerunTestServerAsAdmin(t)
	srv.createSkill(t, "deploy-helper")
	srv.registerSuite(t, "deploy-helper")

	resp := srv.postJSON(t, "/api/skills/deploy-helper/evals", map[string]any{})
	if resp.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", resp.Code, resp.Body)
	}
}

// The orphan case the push-time provenance exists for: a worker claims a job
// with no suite, derives one, pushes it — and then never reports, because its
// lease lapsed or it died. Before provenance was established at push time the
// suite stayed recorded as authored forever, and the *next* run against it
// would score as authored and could clear a scan hold on a real security
// finding.
func TestDerivePush_AnOrphanedPushIsStillRecordedDerived(t *testing.T) {
	env := newRerunEnv(t)
	env.publishSkill(t, "imported-skill")

	if res := env.post(t, "/api/skills/imported-skill/evals", `{}`); res.Code != http.StatusAccepted {
		t.Fatalf("rerun status = %d, want 202: %s", res.Code, res.Body)
	}
	job := env.latestJob(t, "imported-skill")
	if job.SuiteRef != "" {
		t.Fatalf("job suite_ref = %q, want empty for a skill with no suite", job.SuiteRef)
	}

	claimed, token, ok, err := env.queue.Claim(context.Background(), "w1", time.Minute)
	if err != nil || !ok {
		t.Fatalf("Claim = (%v, %v)", ok, err)
	}
	ref := env.registerSuiteForJob(t, "imported-skill", string(claimed.ID), token)

	// No report ever arrives. The suite must already know what it is.
	rec, err := env.suites.Get(context.Background(), ref)
	if err != nil {
		t.Fatalf("Get suite: %v", err)
	}
	if rec.Origin != evalsuite.OriginDerived {
		t.Fatalf("origin = %q for a suite pushed by a derive job's worker, want derived", rec.Origin)
	}
	after, err := env.queue.Get(context.Background(), claimed.ID)
	if err != nil || after == nil {
		t.Fatalf("Get job: %v", err)
	}
	if after.SuiteRef != ref {
		t.Fatalf("job suite_ref = %q after the push, want %q", after.SuiteRef, ref)
	}

	// And the run that comes after it — which finds this suite through
	// LatestForSkill and therefore names it — is still scored as derived, so
	// it can never clear a scan hold.
	if res := env.post(t, "/api/skills/imported-skill/evals", `{}`); res.Code != http.StatusAccepted {
		t.Fatalf("second rerun status = %d, want 202: %s", res.Code, res.Body)
	}
	next := env.latestJob(t, "imported-skill")
	if next.SuiteRef != ref {
		t.Fatalf("the follow-up job named suite %q, want the orphaned %q", next.SuiteRef, ref)
	}
	nextClaimed, nextToken, ok, err := env.queue.Claim(context.Background(), "w2", time.Minute)
	if err != nil || !ok {
		t.Fatalf("Claim = (%v, %v)", ok, err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/eval/jobs/"+string(nextClaimed.ID)+"/report",
		bytes.NewReader(reportFixture("imported-skill", ref, 80)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Claim-Token", nextToken)
	rr := httptest.NewRecorder()
	env.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("report status = %d, want 200: %s", rr.Code, rr.Body)
	}

	sk, err := env.skills.GetByName(context.Background(), "imported-skill")
	if err != nil || sk == nil {
		t.Fatalf("GetByName: %v", err)
	}
	qrec, err := quality.NewStore(env.pool).Latest(context.Background(), sk.ID, nextClaimed.Version)
	if err != nil || qrec == nil {
		t.Fatalf("Latest quality: %v", err)
	}
	if !qrec.SuiteDerived {
		t.Fatal("a score against an orphaned derived suite was recorded as authored — it could clear a scan hold")
	}
}
