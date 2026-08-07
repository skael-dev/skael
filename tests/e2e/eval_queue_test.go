//go:build integration

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/stretchr/testify/require"

	"github.com/skael-dev/skael/cli/client"
	"github.com/skael-dev/skael/internal/auth"
	"github.com/skael-dev/skael/internal/eval/report"
	"github.com/skael-dev/skael/internal/eval/suite"
	"github.com/skael-dev/skael/internal/evalqueue"
	"github.com/skael-dev/skael/internal/evalsuite"
	"github.com/skael-dev/skael/internal/platform"
	"github.com/skael-dev/skael/internal/quality"
	"github.com/skael-dev/skael/internal/skill"
	gosync "github.com/skael-dev/skael/internal/sync"
	"github.com/skael-dev/skael/internal/testutil"
	"github.com/skael-dev/skael/internal/worker"
)

// ---------------------------------------------------------------------------
// evalEnv: a server wired with the eval queue/suite/quality stack, plus the
// thin helpers the eval-queue scenario below drives. This mirrors
// startTestServer's wiring, with the additions internal/server.Build makes
// for eval: evalsuite.Registry, evalqueue.PoolExecutor, quality.Store, and
// the adapters that let skill.RegisterRoutes enqueue on publish.
// ---------------------------------------------------------------------------

type evalEnv struct {
	serverURL string
	apiKey    string
	c         *client.Client
}

// evalQueueAdapter and evalSuiteAdapter mirror internal/server's adapters of
// the same name: internal/skill cannot import internal/evalqueue directly
// (evalqueue imports skill for its own route wiring), so whatever wires both
// packages together has to bridge them itself.
type evalQueueAdapter struct{ q *evalqueue.PoolExecutor }

func (a evalQueueAdapter) Submit(ctx context.Context, j skill.EvalJobRequest) (string, error) {
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

type evalSuiteAdapter struct{ r *evalsuite.Registry }

func (a evalSuiteAdapter) LatestForSkill(ctx context.Context, name string) (*skill.SuiteRecord, error) {
	rec, err := a.r.LatestForSkill(ctx, name)
	if err != nil {
		if err == evalsuite.ErrNotFound {
			return nil, nil
		}
		// evalsuite wraps ErrNotFound with fmt.Errorf/%w, so a plain == above
		// won't catch it; fall through to errors.Is via the wrapped check.
		if isSuiteNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	if rec == nil {
		return nil, nil
	}
	return &skill.SuiteRecord{Ref: rec.Ref}, nil
}

// startServer spins up a fully-wired HTTP server — skills, eval suites, the
// eval job queue, and quality — backed by a real Postgres instance (via
// testcontainers). The API key belongs to the instance owner, since claiming
// a job and re-running an eval are both privileged operations.
func startServer(t *testing.T) *evalEnv {
	t.Helper()
	return startServerWithFloor(t, 0)
}

// startServerWithFloor is startServer with an explicit QUALITY_FLOOR. The
// floor is threaded into both places platform.Config.QualityFloor reaches in
// the real server — skill.RouteOptions (the publish decision) and
// evalqueue.RouteOptions (the re-decision a landed score triggers) — because
// a floor applied in only one of them is exactly the seam these tests exist
// to cover.
func startServerWithFloor(t *testing.T, floor float64) *evalEnv {
	t.Helper()

	pool := testutil.SetupTestDB(t)

	storageDir := t.TempDir()
	storage, err := platform.NewLocalStorage(storageDir)
	require.NoError(t, err)

	userStore := auth.NewUserStore(pool)
	keyStore := auth.NewKeyStore(pool)

	pwHash, err := auth.HashPassword("test-password")
	require.NoError(t, err)
	testUser, err := userStore.CreateWithRole(context.Background(), "eval-e2e@test.local", "Eval E2E Test User", pwHash, auth.RoleOwner)
	require.NoError(t, err)

	fullKey, prefix, err := auth.GenerateAPIKey()
	require.NoError(t, err)
	keyHash, err := auth.HashAPIKey(fullKey)
	require.NoError(t, err)
	_, err = keyStore.Create(context.Background(), testUser.ID, "eval-e2e-test-key", prefix, keyHash)
	require.NoError(t, err)
	apiKey := fullKey

	sessionManager := scs.New()

	router := chi.NewMux()
	router.Use(middleware.Recoverer)
	router.Use(platform.ClientIP(platform.ParseTrustedProxies(os.Getenv("TRUSTED_PROXIES"))))
	router.Use(sessionManager.LoadAndSave)
	router.Use(auth.Middleware(sessionManager, userStore, keyStore))

	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, 10<<20) // 10 MB
			next.ServeHTTP(w, r)
		})
	})

	humaConfig := huma.DefaultConfig("Skael API", "1.0.0")
	api := humachi.New(router, humaConfig)

	huma.Register(api, huma.Operation{
		OperationID: "health",
		Method:      http.MethodGet,
		Path:        "/api/health",
	}, func(ctx context.Context, input *struct{}) (*struct {
		Body struct {
			Status string `json:"status"`
		}
	}, error) {
		out := &struct {
			Body struct {
				Status string `json:"status"`
			}
		}{}
		out.Body.Status = "ok"
		return out, nil
	})

	// Eval queue/suite/quality are constructed ahead of skill.RegisterRoutes
	// so publish can enqueue against them — same order as internal/server.
	skillStore := skill.NewStore(pool)
	evalPool := evalqueue.NewPool(pool)
	qualityStore := quality.NewStore(pool)
	suiteRegistry := evalsuite.NewRegistry(pool, storage)

	skill.RegisterRoutes(api, router, skillStore, storage, skill.RouteOptions{
		Queue:        evalQueueAdapter{q: evalPool},
		Suites:       evalSuiteAdapter{r: suiteRegistry},
		QualityFloor: floor,
	})

	skill.RegisterReviewQueueRoutes(api, skillStore, nil)
	evalsuite.RegisterRoutes(api, router, suiteRegistry, skillStore, evalsuite.RouteOptions{Claims: evalPool})
	evalqueue.RegisterRoutes(api, evalPool, qualityStore, skillStore, suiteRegistry, evalqueue.RouteOptions{
		Releaser:     skill.NewReleaser(skillStore),
		QualityFloor: floor,
	})
	quality.RegisterRoutes(api, qualityStore, skillStore)

	// The sync manifest is one of the latest-resolving paths a held version
	// must stay out of, so it is wired here exactly as internal/server does.
	syncStore := gosync.NewStore(pool)
	huma.Register(api, huma.Operation{
		OperationID: "get-manifest",
		Method:      http.MethodGet,
		Path:        "/api/sync/manifest",
	}, func(ctx context.Context, input *struct{}) (*struct {
		Body []gosync.ManifestEntry
	}, error) {
		entries, err := syncStore.GetManifest(ctx)
		if err != nil {
			return nil, huma.Error500InternalServerError("", err)
		}
		return &struct {
			Body []gosync.ManifestEntry
		}{Body: entries}, nil
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	server := &http.Server{
		Handler:      router,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
	go server.Serve(listener) //nolint:errcheck

	url := "http://" + listener.Addr().String()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	})

	require.Eventually(t, func() bool {
		resp, err := http.Get(url + "/api/health")
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 30*time.Second, 100*time.Millisecond, "server did not become ready in time")

	return &evalEnv{
		serverURL: url,
		apiKey:    apiKey,
		c:         client.New(url, apiKey),
	}
}

// createSkill registers a skill record so a suite can be pushed against it
// and a version published under it.
func (e *evalEnv) createSkill(t *testing.T, name string) {
	t.Helper()
	_, err := e.c.CreateSkill(name, "eval queue e2e test skill")
	require.NoError(t, err)
}

// publishSkill creates and publishes a minimal skill under name with no
// authored eval suite, mirroring how an imported skill arrives — no
// whetstone run, nothing under eval_suites for it yet.
func publishSkill(t *testing.T, srv *evalEnv, name string) {
	t.Helper()
	srv.createSkill(t, name)
	srv.publish(t, name, fixtureBundle(t))
}

// claimJob claims the next queued eval job over HTTP, exactly as
// cmd/skael-worker's poll loop does, and returns it along with the lease
// token the report call must present back.
func claimJob(t *testing.T, srv *evalEnv) (client.EvalJob, string) {
	t.Helper()
	job, token, ok, err := srv.c.ClaimEvalJob("eval-e2e-worker", 0)
	require.NoError(t, err)
	require.True(t, ok, "no claimable job")
	return *job, token
}

// reportJob posts rep as the completed outcome of jobID, using the claim
// token a prior claimJob returned.
func reportJob(t *testing.T, srv *evalEnv, jobID, token string, rep []byte) {
	t.Helper()
	require.NoError(t, srv.c.PostEvalReport(jobID, token, rep))
}

// reportFor builds the minimal report.Report the server's report handler
// accepts for skillName against suiteRef — no members or tasks, since
// nothing here checks them (see quality.FromReport).
func reportFor(t *testing.T, skillName, suiteRef string) []byte {
	t.Helper()
	now := time.Now()
	rep := report.Report{
		SchemaVersion: report.SchemaVersion,
		Skill:         skillName,
		SpecVersion:   1,
		Tier:          "full",
		SuiteRef:      suiteRef,
		EngineVersion: "test",
		Headline:      70.0,
		StartedAt:     now,
		FinishedAt:    now,
	}
	b, err := json.Marshal(rep)
	require.NoError(t, err)
	return b
}

// latestQuality fetches the full quality record for skillName at version,
// via the per-version endpoint — the one that actually carries SuiteDerived
// through to the caller.
func latestQuality(t *testing.T, srv *evalEnv, skillName string, version int) qualityResult {
	t.Helper()
	var out qualityResult
	srv.getJSON(t, fmt.Sprintf("/api/skills/%s/quality/%d", skillName, version), &out)
	return out
}

// pushSuite uploads archive as skillName's eval suite, along with a single
// passing oracle-gate check for the fixture's one task ("t1" — see
// fixtureSuiteArchive), and returns the stored suite's ref.
func (e *evalEnv) pushSuite(t *testing.T, skillName string, archive []byte) string {
	t.Helper()
	checks := []client.EvalSuiteCheck{{TaskID: "t1", OK: true}}
	up, err := e.c.UploadEvalSuite(client.EvalSuiteUploadRequest{Skill: skillName, SpecVersion: 1, Checks: checks, Archive: archive})
	require.NoError(t, err)
	return up.Ref
}

// publishResult is the subset of the publish response this scenario checks.
type publishResult struct {
	Version int `json:"version"`
	Quality struct {
		State string `json:"state,omitempty"`
		JobID string `json:"job_id,omitempty"`
	} `json:"quality"`
}

// publish posts archive as a new version of skillName and returns the
// decoded response, including the quality state the server reports for it.
func (e *evalEnv) publish(t *testing.T, skillName string, archive []byte) publishResult {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, e.serverURL+"/api/skills/"+skillName+"/versions", bytes.NewReader(archive))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/gzip")
	req.Header.Set("X-API-Key", e.apiKey)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var out publishResult
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	return out
}

// rerun calls POST /api/skills/{name}/evals asking for models as the panel,
// and returns the new job's ID.
func (e *evalEnv) rerun(t *testing.T, skillName string, models []string) string {
	t.Helper()
	payload, err := json.Marshal(struct {
		Models []string `json:"models"`
	}{Models: models})
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodPost, e.serverURL+"/api/skills/"+skillName+"/evals", bytes.NewReader(payload))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", e.apiKey)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusAccepted, resp.StatusCode)

	var out struct {
		JobID string `json:"job_id"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	return out.JobID
}

// qualityResult is the subset of a quality record this scenario checks.
type qualityResult struct {
	Headline     float64 `json:"headline_score"`
	Verified     bool    `json:"verified"`
	SuiteRef     string  `json:"suite_ref"`
	SuiteDerived bool    `json:"suite_derived"`
}

// getQuality fetches the most recent quality score for skillName.
func (e *evalEnv) getQuality(t *testing.T, skillName string) qualityResult {
	t.Helper()
	var out qualityResult
	e.getJSON(t, "/api/skills/"+skillName+"/quality", &out)
	return out
}

// getQualityHistory fetches skillName's quality score history, newest first.
func (e *evalEnv) getQualityHistory(t *testing.T, skillName string) []qualityResult {
	t.Helper()
	var body struct {
		History []qualityResult `json:"history"`
	}
	e.getJSON(t, "/api/skills/"+skillName+"/quality/history", &body)
	return body.History
}

func (e *evalEnv) getJSON(t *testing.T, path string, out interface{}) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, e.serverURL+path, nil)
	require.NoError(t, err)
	req.Header.Set("X-API-Key", e.apiKey)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NoError(t, json.NewDecoder(resp.Body).Decode(out))
}

// newWorker builds a worker.Worker talking to this server over HTTP, backed
// by r rather than a Docker-driven runner — this scenario is about the
// queue, the transport, and ingestion, not about whether an LLM scores well.
func (e *evalEnv) newWorker(t *testing.T, r worker.Runner) *worker.Worker {
	t.Helper()
	api := worker.NewHTTPAPI(e.serverURL, e.apiKey)
	w, err := worker.New(worker.Config{
		WorkerID: "eval-e2e-worker",
		WorkRoot: t.TempDir(),
	}, api, r, nil)
	require.NoError(t, err)
	return w
}

// ---------------------------------------------------------------------------
// stubRunner is a test double for worker.Runner: no Docker, no sandbox, just
// a canned headline score. Its SuiteRef is deliberately taken from the
// RunInput the worker hands it — which the worker itself populates from the
// claimed job's own SuiteRef (see internal/worker/worker.go's runJob) — not
// from a field baked into the stub. A report whose SuiteRef the stub invented
// itself would let a mismatched claim/report pairing slip through unnoticed.
// ---------------------------------------------------------------------------

type stubRunner struct {
	headline float64
	// suiteRef, when set, is asserted against the claimed job's SuiteRef as
	// it arrives on RunInput — an extra check that the queue handed the
	// worker the suite the test actually pushed, not a source for the
	// report's own SuiteRef.
	suiteRef string
}

func (s stubRunner) Run(_ context.Context, in worker.RunInput) (*report.Report, error) {
	if s.suiteRef != "" && in.SuiteRef != s.suiteRef {
		panic("stubRunner: claimed job's suite_ref does not match the suite the test pushed")
	}
	now := time.Now()
	return &report.Report{
		SchemaVersion: report.SchemaVersion,
		Skill:         in.Skill,
		SpecVersion:   1,
		Tier:          "full",
		SuiteRef:      in.SuiteRef,
		EngineVersion: "test",
		Headline:      s.headline,
		StartedAt:     now,
		FinishedAt:    now,
	}, nil
}

// ---------------------------------------------------------------------------
// Fixtures: a minimal skill bundle and a minimal eval suite, matching the
// shapes internal/worker's own tests use, so a suite ref the test computes
// matches what the server's suite registry computes from the same archive.
// ---------------------------------------------------------------------------

func fixtureBundle(t *testing.T) []byte {
	t.Helper()
	dir := t.TempDir()
	md := "---\nname: deploy-helper\ndescription: Deploys the thing.\n---\n\n# deploy-helper\n\nDeploys the thing.\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(md), 0o644))
	archive, _, _, err := skill.Pack(dir)
	require.NoError(t, err)
	return archive
}

func writeFixtureSuite(t *testing.T, dir string) {
	t.Helper()
	s := &suite.Suite{
		Tasks: []suite.TaskPkg{
			{
				ID:       "t1",
				Kind:     "happy",
				Split:    "holdout",
				PromptMD: "# Task\n\nDo the thing.\n",
				Oracle:   "#!/bin/sh\necho ok\n",
				Verifier: "#!/bin/sh\nexit 0\n",
			},
		},
		Triggers: suite.TriggerSet{
			Positive: []string{"do the thing"},
			Negative: []string{"do something unrelated"},
		},
	}
	require.NoError(t, s.Write(dir))
}

func fixtureSuiteArchive(t *testing.T) []byte {
	t.Helper()
	dir := t.TempDir()
	writeFixtureSuite(t, dir)
	archive, err := evalsuite.PackDir(dir)
	require.NoError(t, err)
	return archive
}

// isSuiteNotFound reports whether err is (or wraps) evalsuite.ErrNotFound.
func isSuiteNotFound(err error) bool {
	for err != nil {
		if err == evalsuite.ErrNotFound {
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// ---------------------------------------------------------------------------
// Scenario: publish enqueues a job, a worker with a stub runner claims and
// completes it, and the resulting score lands verified in skill_quality —
// then a re-run against a different panel produces a second, comparable row.
// ---------------------------------------------------------------------------

func TestEvalQueue_PublishToVerifiedScore(t *testing.T) {
	env := startServer(t)

	env.createSkill(t, "deploy-helper")
	ref := env.pushSuite(t, "deploy-helper", fixtureSuiteArchive(t))
	pub := env.publish(t, "deploy-helper", fixtureBundle(t))
	if pub.Quality.State != "pending" {
		t.Fatalf("quality state after publish = %q, want pending", pub.Quality.State)
	}

	// A worker with a stub runner: this test is about the queue and ingestion,
	// not about Docker.
	w := env.newWorker(t, stubRunner{headline: 68.25, suiteRef: ref})
	worked, err := w.RunOnce(context.Background())
	if err != nil || !worked {
		t.Fatalf("worker did not run the job: (%v, %v)", worked, err)
	}

	q := env.getQuality(t, "deploy-helper")
	if !q.Verified || q.Headline != 68.25 {
		t.Fatalf("quality = %+v, want a verified 68.25", q)
	}
	if q.SuiteRef != ref {
		t.Fatalf("stored suite_ref = %s, want %s", q.SuiteRef, ref)
	}

	// The regression signal: same suite, different panel, comparable number.
	env.rerun(t, "deploy-helper", []string{"opus-next"})
	w2 := env.newWorker(t, stubRunner{headline: 61.0, suiteRef: ref})
	if worked, err := w2.RunOnce(context.Background()); err != nil || !worked {
		t.Fatalf("re-run job did not execute: (%v, %v)", worked, err)
	}
	hist := env.getQualityHistory(t, "deploy-helper")
	if len(hist) != 2 {
		t.Fatalf("history = %d rows, want 2", len(hist))
	}
	if hist[0].SuiteRef != hist[1].SuiteRef {
		t.Fatal("the re-run used a different suite; the two scores are not comparable")
	}
}

// ---------------------------------------------------------------------------
// Scenario: a skill with no authored suite still evaluates, end to end. The
// server enqueues with an empty suite_ref rather than 404ing, and a worker
// that derives a suite pushes it before reporting — simulated here by
// pushSuite/reportJob directly, since this test is about the queue/suite/
// quality wiring, not about running the real LLM+Docker deriver.
// ---------------------------------------------------------------------------

func TestEvalQueue_SkillWithNoSuiteEnqueuesADeriveJob(t *testing.T) {
	// The whole point: a skill that never went through whetstone can be
	// evaluated from the UI.
	srv := startServer(t)
	publishSkill(t, srv, "imported-skill")
	// deliberately no pushSuite

	jobID := srv.rerun(t, "imported-skill", nil)

	job, token := claimJob(t, srv)
	if job.ID != jobID {
		t.Fatalf("claimed job id = %q, want %q", job.ID, jobID)
	}
	if job.SuiteRef != "" {
		t.Fatalf("claimed job suite_ref = %q, want empty", job.SuiteRef)
	}

	// A worker with a derived suite in hand pushes it, then reports against it.
	ref := srv.pushSuite(t, "imported-skill", fixtureSuiteArchive(t))
	reportJob(t, srv, job.ID, token, reportFor(t, "imported-skill", ref))

	rec := latestQuality(t, srv, "imported-skill", 1)
	if !rec.SuiteDerived {
		t.Fatal("the score was not recorded as derived")
	}
}
