package evalqueue_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/skael-dev/skael/internal/auth"
	"github.com/skael-dev/skael/internal/eval/report"
	"github.com/skael-dev/skael/internal/evalqueue"
	"github.com/skael-dev/skael/internal/evalsuite"
	"github.com/skael-dev/skael/internal/platform"
	"github.com/skael-dev/skael/internal/quality"
	"github.com/skael-dev/skael/internal/skill"
	"github.com/skael-dev/skael/internal/testutil"
)

// testServer wires the real router (auth context attached, evalqueue routes
// registered) for HTTP-level tests.
type testServer struct {
	handler http.Handler
	skills  *skill.Store
	queue   *evalqueue.PoolExecutor
	pool    *pgxpool.Pool
}

func newTestServerWithRole(t *testing.T, role string) *testServer {
	t.Helper()

	pool := testutil.SetupTestDB(t)

	skillStore := skill.NewStore(pool)
	q := evalqueue.NewPool(pool)
	qual := quality.NewStore(pool)

	user := &auth.User{
		ID:    "00000000-0000-0000-0000-0000000000aa",
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
	suiteStorage, err := platform.NewLocalStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStorage: %v", err)
	}
	suiteRegistry := evalsuite.NewRegistry(pool, suiteStorage)
	evalqueue.RegisterRoutes(api, q, qual, skillStore, suiteRegistry)

	return &testServer{handler: r, skills: skillStore, queue: q, pool: pool}
}

// newTestServer is authenticated as a plain member.
func newTestServer(t *testing.T) *testServer {
	t.Helper()
	return newTestServerWithRole(t, auth.RoleMember)
}

// newTestServerAsAdmin is authenticated as an admin (privileged).
func newTestServerAsAdmin(t *testing.T) *testServer {
	t.Helper()
	return newTestServerWithRole(t, auth.RoleAdmin)
}

func (s *testServer) createSkill(t *testing.T, name string) string {
	t.Helper()
	sk, err := s.skills.Create(t.Context(), name, name, "test skill", "# "+name, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("createSkill(%s): %v", name, err)
	}
	return sk.ID
}

func (s *testServer) submitJob(t *testing.T, skillID, skillName string, version int, suiteRef string) string {
	t.Helper()
	id, err := s.queue.Submit(context.Background(), evalqueue.Job{
		SkillID:   skillID,
		SkillName: skillName,
		Version:   version,
		SuiteRef:  suiteRef,
	})
	if err != nil {
		t.Fatalf("submitJob: %v", err)
	}
	return string(id)
}

func (s *testServer) postJSON(t *testing.T, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("postJSON: marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	s.handler.ServeHTTP(rr, req)
	return rr
}

func (s *testServer) postReport(t *testing.T, jobID, token string, rep []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/eval/jobs/"+jobID+"/report", bytes.NewReader(rep))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Claim-Token", token)
	rr := httptest.NewRecorder()
	s.handler.ServeHTTP(rr, req)
	return rr
}

type jobStatus struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

func (s *testServer) getJob(t *testing.T, jobID string) jobStatus {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/eval/jobs/"+jobID, nil)
	rr := httptest.NewRecorder()
	s.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("getJob(%s): status = %d: %s", jobID, rr.Code, rr.Body)
	}
	var js jobStatus
	if err := json.Unmarshal(rr.Body.Bytes(), &js); err != nil {
		t.Fatalf("getJob(%s): unmarshal: %v", jobID, err)
	}
	return js
}

func claimToken(t *testing.T, resp *httptest.ResponseRecorder) string {
	t.Helper()
	var claimed struct {
		ClaimToken string `json:"claim_token"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &claimed); err != nil {
		t.Fatalf("claimToken: unmarshal: %v: %s", err, resp.Body)
	}
	if claimed.ClaimToken == "" {
		t.Fatalf("claimToken: empty token in %s", resp.Body)
	}
	return claimed.ClaimToken
}

// reportFixture builds a minimal valid report.Report JSON body matching the
// current schema version.
func reportFixture(skillName, suiteRef string, headline float64) []byte {
	rep := report.Report{
		SchemaVersion: report.SchemaVersion,
		Skill:         skillName,
		SpecVersion:   1,
		Tier:          "full",
		SuiteRef:      suiteRef,
		EngineVersion: "0.9.1",
		ModelPanel:    []report.PanelMember{{Agent: "claude-code", Model: "opus", Class: "strong"}},
		PanelComplete: true,
		Headline:      headline,
		HeadlineCI:    [2]float64{headline - 5, headline + 5},
		Members:       []report.MemberReport{{Healthy: true, DriftGrade: "B"}},
		StartedAt:     time.Now().Add(-time.Minute),
		FinishedAt:    time.Now(),
	}
	b, err := json.Marshal(rep)
	if err != nil {
		panic(err)
	}
	return b
}

func TestClaimRoute_RequiresPrivilege(t *testing.T) {
	srv := newTestServer(t) // authenticated as a plain member
	resp := srv.postJSON(t, "/api/eval/jobs/claim", map[string]any{"worker_id": "w1", "lease_seconds": 60})
	if resp.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 — a member must not be able to drain the queue", resp.Code)
	}
}

func TestClaimRoute_EmptyQueueIs204(t *testing.T) {
	srv := newTestServerAsAdmin(t)
	resp := srv.postJSON(t, "/api/eval/jobs/claim", map[string]any{"worker_id": "w1", "lease_seconds": 60})
	if resp.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.Code)
	}
}

func TestReportRoute_WritesAVerifiedScore(t *testing.T) {
	srv := newTestServerAsAdmin(t)
	skillID := srv.createSkill(t, "deploy-helper")
	jobID := srv.submitJob(t, skillID, "deploy-helper", 2, "sha256:abc")

	claim := srv.postJSON(t, "/api/eval/jobs/claim", map[string]any{"worker_id": "w1", "lease_seconds": 600})
	var claimed struct {
		Job        struct{ ID string } `json:"job"`
		ClaimToken string              `json:"claim_token"`
	}
	_ = json.Unmarshal(claim.Body.Bytes(), &claimed)
	if claimed.Job.ID != jobID || claimed.ClaimToken == "" {
		t.Fatalf("claim response = %s", claim.Body)
	}

	rep := reportFixture("deploy-helper", "sha256:abc", 72.5)
	resp := srv.postReport(t, jobID, claimed.ClaimToken, rep)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", resp.Code, resp.Body)
	}

	q := quality.NewStore(srv.pool)
	rec, err := q.Latest(context.Background(), skillID, 2)
	if err != nil || rec == nil {
		t.Fatalf("no quality row was written: %v", err)
	}
	if !rec.Verified {
		t.Fatal("a worker-posted score was not marked verified")
	}
	if rec.Headline != 72.5 {
		t.Fatalf("headline = %v, want 72.5", rec.Headline)
	}
	job := srv.getJob(t, jobID)
	if job.Status != "done" {
		t.Fatalf("job status = %q, want done", job.Status)
	}
}

func TestReportRoute_RejectsAForgedClaimToken(t *testing.T) {
	srv := newTestServerAsAdmin(t)
	skillID := srv.createSkill(t, "deploy-helper")
	jobID := srv.submitJob(t, skillID, "deploy-helper", 2, "sha256:abc")
	_ = srv.postJSON(t, "/api/eval/jobs/claim", map[string]any{"worker_id": "w1", "lease_seconds": 600})

	resp := srv.postReport(t, jobID, "0000000000000000", reportFixture("deploy-helper", "sha256:abc", 99))
	if resp.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.Code)
	}
	q := quality.NewStore(srv.pool)
	if rec, _ := q.Latest(context.Background(), skillID, 2); rec != nil {
		t.Fatal("a forged token wrote a score")
	}
}

// The report must describe the job it claims to answer. Otherwise a worker can
// post a score computed against a different suite and it lands as this
// version's number.
func TestReportRoute_RejectsAMismatchedSuiteRef(t *testing.T) {
	srv := newTestServerAsAdmin(t)
	skillID := srv.createSkill(t, "deploy-helper")
	jobID := srv.submitJob(t, skillID, "deploy-helper", 2, "sha256:abc")
	claim := srv.postJSON(t, "/api/eval/jobs/claim", map[string]any{"worker_id": "w1", "lease_seconds": 600})
	token := claimToken(t, claim)

	resp := srv.postReport(t, jobID, token, reportFixture("deploy-helper", "sha256:OTHER", 99))
	if resp.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.Code)
	}
}

func TestReportRoute_RejectsAReportForAnotherSkill(t *testing.T) {
	srv := newTestServerAsAdmin(t)
	skillID := srv.createSkill(t, "deploy-helper")
	jobID := srv.submitJob(t, skillID, "deploy-helper", 2, "sha256:abc")
	claim := srv.postJSON(t, "/api/eval/jobs/claim", map[string]any{"worker_id": "w1", "lease_seconds": 600})
	token := claimToken(t, claim)

	resp := srv.postReport(t, jobID, token, reportFixture("other-skill", "sha256:abc", 99))
	if resp.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.Code)
	}
}

func TestFailRoute_RequeuesWithRetriesRemaining(t *testing.T) {
	srv := newTestServerAsAdmin(t)
	skillID := srv.createSkill(t, "deploy-helper")
	jobID := srv.submitJob(t, skillID, "deploy-helper", 2, "sha256:abc")
	claim := srv.postJSON(t, "/api/eval/jobs/claim", map[string]any{"worker_id": "w1", "lease_seconds": 600})
	token := claimToken(t, claim)

	req := httptest.NewRequest(http.MethodPost, "/api/eval/jobs/"+jobID+"/fail", bytes.NewReader([]byte(`{"error":"sandbox unavailable"}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Claim-Token", token)
	rr := httptest.NewRecorder()
	srv.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body)
	}

	job := srv.getJob(t, jobID)
	if job.Status != "queued" {
		t.Fatalf("job status = %q, want queued (retries remain)", job.Status)
	}
}

func TestFailRoute_RejectsAForgedClaimToken(t *testing.T) {
	srv := newTestServerAsAdmin(t)
	skillID := srv.createSkill(t, "deploy-helper")
	jobID := srv.submitJob(t, skillID, "deploy-helper", 2, "sha256:abc")
	_ = srv.postJSON(t, "/api/eval/jobs/claim", map[string]any{"worker_id": "w1", "lease_seconds": 600})

	req := httptest.NewRequest(http.MethodPost, "/api/eval/jobs/"+jobID+"/fail", bytes.NewReader([]byte(`{"error":"x"}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Claim-Token", "0000000000000000")
	rr := httptest.NewRecorder()
	srv.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
}

func TestGetJobRoute_UnknownIDIs404(t *testing.T) {
	srv := newTestServerAsAdmin(t)
	req := httptest.NewRequest(http.MethodGet, "/api/eval/jobs/00000000-0000-0000-0000-000000000000", nil)
	rr := httptest.NewRecorder()
	srv.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rr.Code, rr.Body)
	}
}

func TestReportRoute_MalformedBodyIs400(t *testing.T) {
	srv := newTestServerAsAdmin(t)
	skillID := srv.createSkill(t, "deploy-helper")
	jobID := srv.submitJob(t, skillID, "deploy-helper", 2, "sha256:abc")
	claim := srv.postJSON(t, "/api/eval/jobs/claim", map[string]any{"worker_id": "w1", "lease_seconds": 600})
	token := claimToken(t, claim)

	resp := srv.postReport(t, jobID, token, []byte(`{not-json`))
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", resp.Code, resp.Body)
	}
}

func TestHeartbeatRoute_409AfterCancel(t *testing.T) {
	srv := newTestServerAsAdmin(t)
	skillID := srv.createSkill(t, "deploy-helper")
	jobID := srv.submitJob(t, skillID, "deploy-helper", 2, "sha256:abc")
	claim := srv.postJSON(t, "/api/eval/jobs/claim", map[string]any{"worker_id": "w1", "lease_seconds": 600})
	token := claimToken(t, claim)

	if err := srv.queue.Cancel(context.Background(), evalqueue.JobID(jobID)); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/eval/jobs/"+jobID+"/heartbeat", nil)
	req.Header.Set("X-Claim-Token", token)
	rr := httptest.NewRecorder()
	srv.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rr.Code)
	}
}

// A lapsed lease is the lease being lost: the job row is still "running"
// (nobody has reclaimed it yet) but VerifyClaim already refuses it because
// lease_expires_at has passed. The worker needs 409, not 403, so it knows to
// abandon cleanly rather than treating this as an auth failure.
func TestHeartbeatRoute_409OnLapsedLeaseStillMarkedRunning(t *testing.T) {
	srv := newTestServerAsAdmin(t)
	skillID := srv.createSkill(t, "deploy-helper")
	jobID := srv.submitJob(t, skillID, "deploy-helper", 2, "sha256:abc")

	// Claim directly against the queue with a zero-second lease: it is
	// "running" but already lapsed the instant it is granted, and nobody
	// has reclaimed it yet.
	j, token, ok, err := srv.queue.Claim(context.Background(), "w1", 0)
	if err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	if string(j.ID) != jobID {
		t.Fatalf("claimed job = %s, want %s", j.ID, jobID)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/eval/jobs/"+jobID+"/heartbeat", nil)
	req.Header.Set("X-Claim-Token", token)
	rr := httptest.NewRecorder()
	srv.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rr.Code)
	}
}
