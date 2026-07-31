package skill_test

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

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/skael-dev/skael/internal/eval/suite"
	"github.com/skael-dev/skael/internal/evalqueue"
	"github.com/skael-dev/skael/internal/evalsuite"
	"github.com/skael-dev/skael/internal/platform"
	"github.com/skael-dev/skael/internal/skill"
	"github.com/skael-dev/skael/internal/testutil"
)

// enqueueTestServer wires a real router with skill routes registered, backed
// by a real Postgres database, so publish can be exercised end to end
// including the enqueue step.
type enqueueTestServer struct {
	handler http.Handler
	skills  *skill.Store
	suites  *evalsuite.Registry
	pool    *pgxpool.Pool
}

// queueSubmitterFunc adapts a func to skill.QueueSubmitter.
type queueSubmitterFunc func(ctx context.Context, j skill.EvalJobRequest) (string, error)

func (f queueSubmitterFunc) Submit(ctx context.Context, j skill.EvalJobRequest) (string, error) {
	return f(ctx, j)
}

// suiteLookupAdapter adapts a real *evalsuite.Registry to skill.SuiteLookup.
// It mirrors internal/server's evalSuiteAdapter: evalsuite.LatestForSkill
// reports "no suite" as an error wrapping evalsuite.ErrNotFound rather than
// (nil, nil), so that case must be translated — anything else is a genuine
// lookup failure and must be reported as an error, not swallowed as absence.
type suiteLookupAdapter struct {
	r *evalsuite.Registry
}

func (a suiteLookupAdapter) LatestForSkill(ctx context.Context, name string) (*skill.SuiteRecord, error) {
	rec, err := a.r.LatestForSkill(ctx, name)
	if err != nil {
		if errors.Is(err, evalsuite.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if rec == nil {
		return nil, nil
	}
	return &skill.SuiteRecord{Ref: rec.Ref}, nil
}

// suiteLookupFunc adapts a func to skill.SuiteLookup, for tests that need to
// simulate an infrastructure failure without a real registry.
type suiteLookupFunc func(ctx context.Context, name string) (*skill.SuiteRecord, error)

func (f suiteLookupFunc) LatestForSkill(ctx context.Context, name string) (*skill.SuiteRecord, error) {
	return f(ctx, name)
}

// newEnqueueTestServer wires a real router with skill and eval-suite routes
// registered over a fresh database, with a caller-supplied queue and an
// optional suite lookup override. When suites is nil, the real
// suiteLookupAdapter over this server's own evalsuite.Registry is used, so
// registerSuite's uploads are visible to publish exactly as in production.
func newEnqueueTestServer(t *testing.T, queue skill.QueueSubmitter, suites skill.SuiteLookup) *enqueueTestServer {
	t.Helper()

	pool := testutil.SetupTestDB(t)
	store := skill.NewStore(pool)

	storage, err := platform.NewLocalStorage(t.TempDir())
	require.NoError(t, err)

	suiteStorage, err := platform.NewLocalStorage(t.TempDir())
	require.NoError(t, err)
	suiteRegistry := evalsuite.NewRegistry(pool, suiteStorage)

	if suites == nil {
		suites = suiteLookupAdapter{r: suiteRegistry}
	}

	r := chi.NewMux()
	api := humachi.New(r, huma.DefaultConfig("Test API", "1.0.0"))
	skill.RegisterRoutes(api, r, store, storage, skill.RouteOptions{
		Queue:  queue,
		Suites: suites,
	})
	evalsuite.RegisterRoutes(api, r, suiteRegistry, store)

	return &enqueueTestServer{handler: r, skills: store, suites: suiteRegistry, pool: pool}
}

// newTestServer wires a real evalqueue.PoolExecutor as the queue and the real
// suite registry lookup — the production wiring, minus the HTTP layer.
func newTestServer(t *testing.T) *enqueueTestServer {
	t.Helper()

	// Submit needs a pool shared with the server's own database, so build
	// the queue after the server so it can reuse srv.pool.
	var q *evalqueue.PoolExecutor
	queue := queueSubmitterFunc(func(ctx context.Context, j skill.EvalJobRequest) (string, error) {
		id, err := q.Submit(ctx, evalqueue.Job{
			SkillID: j.SkillID, SkillName: j.SkillName, Version: j.Version,
			SuiteRef: j.SuiteRef, Tier: j.Tier, RequestedBy: j.RequestedBy,
		})
		return string(id), err
	})

	srv := newEnqueueTestServer(t, queue, nil)
	q = evalqueue.NewPool(srv.pool)
	return srv
}

// newTestServerWithFailingQueue wires a real suite lookup but a queue whose
// Submit always errors, to prove an enqueue failure never fails a publish.
func newTestServerWithFailingQueue(t *testing.T) *enqueueTestServer {
	t.Helper()
	return newEnqueueTestServer(t, queueSubmitterFunc(func(ctx context.Context, j skill.EvalJobRequest) (string, error) {
		return "", context.DeadlineExceeded
	}), nil)
}

// newTestServerWithFailingSuiteLookup wires a suite lookup that always
// returns a generic (non-ErrNotFound) error, to prove an infrastructure
// failure in the suite lookup is reported and degrades publish to "none"
// rather than being silently indistinguishable from "no suite registered".
func newTestServerWithFailingSuiteLookup(t *testing.T) *enqueueTestServer {
	t.Helper()
	failing := suiteLookupFunc(func(ctx context.Context, name string) (*skill.SuiteRecord, error) {
		return nil, errors.New("suite lookup: connection reset")
	})
	submitCalled := false
	srv := newEnqueueTestServer(t, queueSubmitterFunc(func(ctx context.Context, j skill.EvalJobRequest) (string, error) {
		submitCalled = true
		return "job-should-not-be-submitted", nil
	}), failing)
	t.Cleanup(func() {
		if submitCalled {
			t.Error("Submit must not be called when the suite lookup itself failed")
		}
	})
	return srv
}

func (s *enqueueTestServer) createSkill(t *testing.T, name string) {
	t.Helper()
	_, err := s.skills.Create(context.Background(), name, name, "test skill", "", json.RawMessage(`{}`))
	require.NoError(t, err)
}

// registerSuite uploads a minimal fixture suite for name via a real HTTP
// call to the eval suite registry, so publish's LatestForSkill lookup finds
// it exactly the way it would in production.
func (s *enqueueTestServer) registerSuite(t *testing.T, name string) {
	t.Helper()

	dir := t.TempDir()
	sfx := &suite.Suite{
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
	require.NoError(t, sfx.Write(dir))
	archive, err := evalsuite.PackDir(dir)
	require.NoError(t, err)

	body, err := json.Marshal(struct {
		Skill       string `json:"skill"`
		SpecVersion int    `json:"spec_version"`
		Checks      []struct {
			TaskID string `json:"task_id"`
			OK     bool   `json:"ok"`
		} `json:"checks"`
		ArchiveBase64 string `json:"archive_base64"`
	}{
		Skill:       name,
		SpecVersion: 1,
		Checks: []struct {
			TaskID string `json:"task_id"`
			OK     bool   `json:"ok"`
		}{{TaskID: "t1", OK: true}},
		ArchiveBase64: base64.StdEncoding.EncodeToString(archive),
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/eval/suites", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	s.handler.ServeHTTP(rr, req)
	require.Equal(t, http.StatusCreated, rr.Code, "registerSuite(%s): %s", name, rr.Body.String())
}

func (s *enqueueTestServer) publish(t *testing.T, name string, archive []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/skills/"+name+"/versions", bytes.NewReader(archive))
	req.Header.Set("Content-Type", "application/gzip")
	rr := httptest.NewRecorder()
	s.handler.ServeHTTP(rr, req)
	return rr
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
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(skillMD), 0o644))
	archive, _, _, err := skill.Pack(dir)
	require.NoError(t, err)
	return archive
}

func TestPublish_EnqueuesAnEvalWhenASuiteIsRegistered(t *testing.T) {
	srv := newTestServer(t)
	srv.createSkill(t, "deploy-helper")
	srv.registerSuite(t, "deploy-helper")

	resp := srv.publish(t, "deploy-helper", fixtureBundle(t))
	if resp.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", resp.Code, resp.Body)
	}
	var out struct {
		Quality struct {
			State string `json:"state"`
			JobID string `json:"job_id"`
		} `json:"quality"`
	}
	_ = json.Unmarshal(resp.Body.Bytes(), &out)
	if out.Quality.State != "pending" || out.Quality.JobID == "" {
		t.Fatalf("quality = %+v, want pending with a job id", out.Quality)
	}
}

// No suite means no measurement is possible. That must read as "none", not as
// a job that can never run — a queue full of unrunnable jobs is worse than an
// honest absence.
func TestPublish_NoSuiteMeansNoJob(t *testing.T) {
	srv := newTestServer(t)
	srv.createSkill(t, "deploy-helper")
	resp := srv.publish(t, "deploy-helper", fixtureBundle(t))
	var out struct {
		Quality struct {
			State string `json:"state"`
			JobID string `json:"job_id"`
		} `json:"quality"`
	}
	_ = json.Unmarshal(resp.Body.Bytes(), &out)
	if out.Quality.State != "none" || out.Quality.JobID != "" {
		t.Fatalf("quality = %+v, want none with no job", out.Quality)
	}
	var n int
	require.NoError(t, srv.pool.QueryRow(context.Background(), `SELECT count(*) FROM eval_jobs`).Scan(&n))
	if n != 0 {
		t.Fatalf("%d jobs enqueued with no suite", n)
	}
}

// Enqueue failure must never fail a publish: the archive is stored and the
// version is created before the queue is touched, and the response degrades
// to "none" rather than claiming a job that was never actually created.
func TestPublish_SucceedsWhenEnqueueFails(t *testing.T) {
	srv := newTestServerWithFailingQueue(t)
	srv.createSkill(t, "deploy-helper")
	srv.registerSuite(t, "deploy-helper")
	resp := srv.publish(t, "deploy-helper", fixtureBundle(t))
	if resp.Code != http.StatusCreated {
		t.Fatalf("a queue outage broke publishing: %d %s", resp.Code, resp.Body)
	}

	var out struct {
		Version int `json:"version"`
		Quality struct {
			State string `json:"state"`
			JobID string `json:"job_id"`
		} `json:"quality"`
	}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &out))
	if out.Quality.State != "none" || out.Quality.JobID != "" {
		t.Fatalf("quality = %+v, want none with no job id — the queue outage must not be reported as a real job", out.Quality)
	}

	// The version and archive must have actually survived the enqueue
	// failure, not merely the HTTP status code.
	sk, err := srv.skills.GetByName(context.Background(), "deploy-helper")
	require.NoError(t, err)
	require.NotNil(t, sk)
	if sk.LatestVersion != out.Version {
		t.Fatalf("skill latest_version = %d, want %d — the version row did not survive the enqueue failure", sk.LatestVersion, out.Version)
	}
	ver, err := srv.skills.GetVersion(context.Background(), "deploy-helper", out.Version)
	require.NoError(t, err)
	require.NotNil(t, ver, "published version must be persisted even when enqueue fails")
	if ver.ArchivePath == "" {
		t.Fatal("published version has no archive path — the archive did not survive the enqueue failure")
	}
}

// A suite-lookup failure is an infrastructure problem, not "this skill has
// no suite" — evalsuite.LatestForSkill reports absence as an error wrapping
// ErrNotFound, so any other error must not be silently treated the same way.
// The publish must still succeed (never fail on this), degrading to "none",
// and must never reach Submit with a suite ref it never actually confirmed.
func TestPublish_SuiteLookupFailureDoesNotBlockPublish(t *testing.T) {
	srv := newTestServerWithFailingSuiteLookup(t)
	srv.createSkill(t, "deploy-helper")

	resp := srv.publish(t, "deploy-helper", fixtureBundle(t))
	if resp.Code != http.StatusCreated {
		t.Fatalf("a suite lookup failure broke publishing: %d %s", resp.Code, resp.Body)
	}

	var out struct {
		Quality struct {
			State string `json:"state"`
			JobID string `json:"job_id"`
		} `json:"quality"`
	}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &out))
	if out.Quality.State != "none" || out.Quality.JobID != "" {
		t.Fatalf("quality = %+v, want none with no job", out.Quality)
	}
}

// Republishing byte-identical content is a checksum short-circuit that
// returns before the enqueue step even runs — but the response must still
// report a valid quality state. A zero-value qualityState serializes as
// {"quality":{}}, a third, undocumented state indistinguishable from a
// client's zero-value parsing of "pending" or "none"; a CI job that
// republishes an unchanged skill on every green build would hit this on
// every run.
func TestPublish_IdempotentRepublishReportsQualityNone(t *testing.T) {
	srv := newTestServer(t)
	srv.createSkill(t, "deploy-helper")
	bundle := fixtureBundle(t)

	first := srv.publish(t, "deploy-helper", bundle)
	require.Equal(t, http.StatusCreated, first.Code, first.Body.String())

	second := srv.publish(t, "deploy-helper", bundle)
	require.Equal(t, http.StatusCreated, second.Code, second.Body.String())

	var out struct {
		Created bool `json:"created"`
		Quality struct {
			State string `json:"state"`
			JobID string `json:"job_id"`
		} `json:"quality"`
	}
	require.NoError(t, json.Unmarshal(second.Body.Bytes(), &out))
	if out.Created {
		t.Fatal("expected the checksum short-circuit (created=false) for a byte-identical republish")
	}
	if out.Quality.State != "none" || out.Quality.JobID != "" {
		t.Fatalf("quality = %+v, want none with no job on an idempotent republish", out.Quality)
	}
}
