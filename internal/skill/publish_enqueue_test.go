package skill_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
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
type suiteLookupAdapter struct {
	r *evalsuite.Registry
}

func (a suiteLookupAdapter) LatestForSkill(ctx context.Context, name string) (*skill.SuiteRecord, error) {
	rec, err := a.r.LatestForSkill(ctx, name)
	if err != nil || rec == nil {
		return nil, err
	}
	return &skill.SuiteRecord{Ref: rec.Ref}, nil
}

func newEnqueueTestServer(t *testing.T, queue skill.QueueSubmitter) *enqueueTestServer {
	t.Helper()

	pool := testutil.SetupTestDB(t)
	store := skill.NewStore(pool)

	storage, err := platform.NewLocalStorage(t.TempDir())
	require.NoError(t, err)

	suiteStorage, err := platform.NewLocalStorage(t.TempDir())
	require.NoError(t, err)
	suites := evalsuite.NewRegistry(pool, suiteStorage)

	r := chi.NewMux()
	api := humachi.New(r, huma.DefaultConfig("Test API", "1.0.0"))
	skill.RegisterRoutes(api, r, store, storage, skill.RouteOptions{
		Queue:  queue,
		Suites: suiteLookupAdapter{r: suites},
	})
	evalsuite.RegisterRoutes(api, r, suites, store)

	return &enqueueTestServer{handler: r, skills: store, suites: suites, pool: pool}
}

// newTestServer wires a real evalqueue.PoolExecutor as the queue, sharing the
// same database the skill/suite stores use.
func newTestServer(t *testing.T) *enqueueTestServer {
	t.Helper()

	pool := testutil.SetupTestDB(t)
	store := skill.NewStore(pool)

	storage, err := platform.NewLocalStorage(t.TempDir())
	require.NoError(t, err)
	suiteStorage, err := platform.NewLocalStorage(t.TempDir())
	require.NoError(t, err)
	suites := evalsuite.NewRegistry(pool, suiteStorage)
	q := evalqueue.NewPool(pool)

	queue := queueSubmitterFunc(func(ctx context.Context, j skill.EvalJobRequest) (string, error) {
		id, err := q.Submit(ctx, evalqueue.Job{
			SkillID: j.SkillID, SkillName: j.SkillName, Version: j.Version,
			SuiteRef: j.SuiteRef, Tier: j.Tier, RequestedBy: j.RequestedBy,
		})
		return string(id), err
	})

	r := chi.NewMux()
	api := humachi.New(r, huma.DefaultConfig("Test API", "1.0.0"))
	skill.RegisterRoutes(api, r, store, storage, skill.RouteOptions{
		Queue:  queue,
		Suites: suiteLookupAdapter{r: suites},
	})
	evalsuite.RegisterRoutes(api, r, suites, store)

	return &enqueueTestServer{handler: r, skills: store, suites: suites, pool: pool}
}

// newTestServerWithFailingQueue wires a queue whose Submit always errors, to
// prove an enqueue failure never fails a publish.
func newTestServerWithFailingQueue(t *testing.T) *enqueueTestServer {
	t.Helper()
	return newEnqueueTestServer(t, queueSubmitterFunc(func(ctx context.Context, j skill.EvalJobRequest) (string, error) {
		return "", context.DeadlineExceeded
	}))
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
// version is created before the queue is touched.
func TestPublish_SucceedsWhenEnqueueFails(t *testing.T) {
	srv := newTestServerWithFailingQueue(t)
	srv.createSkill(t, "deploy-helper")
	srv.registerSuite(t, "deploy-helper")
	resp := srv.publish(t, "deploy-helper", fixtureBundle(t))
	if resp.Code != http.StatusCreated {
		t.Fatalf("a queue outage broke publishing: %d %s", resp.Code, resp.Body)
	}
}
