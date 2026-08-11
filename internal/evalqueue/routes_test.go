package evalqueue_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/skael-dev/skael/internal/auth"
	"github.com/skael-dev/skael/internal/eval/report"
	"github.com/skael-dev/skael/internal/eval/suite"
	"github.com/skael-dev/skael/internal/evalqueue"
	"github.com/skael-dev/skael/internal/evalsuite"
	"github.com/skael-dev/skael/internal/gate"
	"github.com/skael-dev/skael/internal/platform"
	"github.com/skael-dev/skael/internal/quality"
	"github.com/skael-dev/skael/internal/scan"
	"github.com/skael-dev/skael/internal/skill"
	"github.com/skael-dev/skael/internal/testutil"
)

// testServer wires the real router (auth context attached, evalqueue routes
// registered) for HTTP-level tests.
type testServer struct {
	handler http.Handler
	skills  *skill.Store
	queue   *evalqueue.PoolExecutor
	suites  *evalsuite.Registry
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
	// A non-zero floor so the ingestion tests can tell "cleared" from "did
	// not clear"; a zero floor would make every verified score clear.
	evalqueue.RegisterRoutes(api, q, qual, skillStore, suiteRegistry, evalqueue.RouteOptions{
		Releaser:     skill.NewReleaser(skillStore),
		QualityFloor: testQualityFloor,
	})

	return &testServer{handler: r, skills: skillStore, queue: q, suites: suiteRegistry, pool: pool}
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

// pushSuite registers a fixture suite for skillName in the registry and
// returns its content-addressed ref. Any report-route test that names a
// suite_ref must have a real registered suite behind it, now that the report
// handler looks the ref up rather than trusting a bare string match.
func (s *testServer) pushSuite(t *testing.T, skillName string) string {
	t.Helper()
	dir := t.TempDir()
	sp := &suite.EvalSet{
		SkillName: skillName,
		Evals: []suite.Eval{
			{ID: 1, Prompt: "Do the thing for " + skillName + ".", Expectations: []string{"it did the thing"}},
		},
	}
	if err := suite.WriteEvalSet(dir, sp); err != nil {
		t.Fatalf("pushSuite: WriteEvalSet: %v", err)
	}
	archive, err := evalsuite.PackDir(dir)
	if err != nil {
		t.Fatalf("pushSuite: PackDir: %v", err)
	}
	rec, err := s.suites.Put(context.Background(), skillName, archive, []evalsuite.Check{{TaskID: "t1", OK: true}}, 1, "test@example.com", nil)
	if err != nil {
		t.Fatalf("pushSuite: Put: %v", err)
	}
	return rec.Ref
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
	ID        string `json:"id"`
	Status    string `json:"status"`
	LastError string `json:"last_error"`
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
		Members:       []report.MemberReport{{Healthy: true, Effectiveness: 80}},
		StartedAt:     time.Now().Add(-time.Minute),
		FinishedAt:    time.Now(),
	}
	b, err := json.Marshal(rep)
	if err != nil {
		panic(err)
	}
	return b
}

// reportFixtureWith builds the same fixture as reportFixture but lets a test
// override the engine version and/or finished-at timestamp, to cover the
// server-side validation and normalization of those two fields.
func reportFixtureWith(skillName, suiteRef string, headline float64, engineVersion string, finishedAt time.Time) []byte {
	rep := report.Report{
		SchemaVersion: report.SchemaVersion,
		Skill:         skillName,
		SpecVersion:   1,
		Tier:          "full",
		SuiteRef:      suiteRef,
		EngineVersion: engineVersion,
		ModelPanel:    []report.PanelMember{{Agent: "claude-code", Model: "opus", Class: "strong"}},
		PanelComplete: true,
		Headline:      headline,
		Members:       []report.MemberReport{{Healthy: true, Effectiveness: 80}},
		StartedAt:     finishedAt.Add(-time.Minute),
		FinishedAt:    finishedAt,
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

// The whole point of the re-run endpoint's agents/models parameters is
// choosing a panel; if the wire format a worker claims a job through cannot
// carry it back, every job silently runs against the worker's default panel
// instead of the one that was asked for.
func TestClaimRoute_CarriesThePanelTheJobWasSubmittedWith(t *testing.T) {
	srv := newTestServerAsAdmin(t)
	skillID := srv.createSkill(t, "deploy-helper")
	_, err := srv.queue.Submit(context.Background(), evalqueue.Job{
		SkillID:   skillID,
		SkillName: "deploy-helper",
		Version:   1,
		SuiteRef:  "sha256:abc",
		Panel:     evalqueue.Panel{Agents: []string{"claude-code"}, Models: []string{"opus"}},
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	resp := srv.postJSON(t, "/api/eval/jobs/claim", map[string]any{"worker_id": "w1", "lease_seconds": 60})
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.Code, resp.Body)
	}
	var claimed struct {
		Job struct {
			Agents []string `json:"agents"`
			Models []string `json:"models"`
		} `json:"job"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &claimed); err != nil {
		t.Fatalf("unmarshal: %v: %s", err, resp.Body)
	}
	if len(claimed.Job.Agents) != 1 || claimed.Job.Agents[0] != "claude-code" {
		t.Fatalf("claimed job agents = %v, want [claude-code]", claimed.Job.Agents)
	}
	if len(claimed.Job.Models) != 1 || claimed.Job.Models[0] != "opus" {
		t.Fatalf("claimed job models = %v, want [opus]", claimed.Job.Models)
	}
}

func TestReportRoute_WritesAVerifiedScore(t *testing.T) {
	srv := newTestServerAsAdmin(t)
	skillID := srv.createSkill(t, "deploy-helper")
	ref := srv.pushSuite(t, "deploy-helper")
	jobID := srv.submitJob(t, skillID, "deploy-helper", 2, ref)

	claim := srv.postJSON(t, "/api/eval/jobs/claim", map[string]any{"worker_id": "w1", "lease_seconds": 600})
	var claimed struct {
		Job        struct{ ID string } `json:"job"`
		ClaimToken string              `json:"claim_token"`
	}
	_ = json.Unmarshal(claim.Body.Bytes(), &claimed)
	if claimed.Job.ID != jobID || claimed.ClaimToken == "" {
		t.Fatalf("claim response = %s", claim.Body)
	}

	rep := reportFixture("deploy-helper", ref, 72.5)
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

// A worker built without -X main.version=... reports engine_version "dev";
// storing that constant would make report.Comparable's engine-version check
// unable to ever fire, silently charting scores from different worker
// builds as one trend. The server must reject it, not store it.
func TestReportRoute_RejectsDevEngineVersion(t *testing.T) {
	srv := newTestServerAsAdmin(t)
	skillID := srv.createSkill(t, "deploy-helper")
	ref := srv.pushSuite(t, "deploy-helper")
	jobID := srv.submitJob(t, skillID, "deploy-helper", 2, ref)
	claim := srv.postJSON(t, "/api/eval/jobs/claim", map[string]any{"worker_id": "w1", "lease_seconds": 600})
	token := claimToken(t, claim)

	resp := srv.postReport(t, jobID, token, reportFixtureWith("deploy-helper", ref, 99, "dev", time.Now()))
	if resp.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", resp.Code, resp.Body)
	}
}

func TestReportRoute_RejectsEmptyEngineVersion(t *testing.T) {
	srv := newTestServerAsAdmin(t)
	skillID := srv.createSkill(t, "deploy-helper")
	ref := srv.pushSuite(t, "deploy-helper")
	jobID := srv.submitJob(t, skillID, "deploy-helper", 2, ref)
	claim := srv.postJSON(t, "/api/eval/jobs/claim", map[string]any{"worker_id": "w1", "lease_seconds": 600})
	token := claimToken(t, claim)

	resp := srv.postReport(t, jobID, token, reportFixtureWith("deploy-helper", ref, 99, "", time.Now()))
	if resp.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", resp.Code, resp.Body)
	}
}

// scored_at must be set server-side, not taken from the report's
// finished_at: that field is worker-supplied and is the sole ordering key
// for Latest. A zero finished_at (an omitted field) or a far-future one (a
// worker with a fast clock) must not affect where the score sorts.
func TestReportRoute_ScoredAtIsServerSideNotReportSupplied(t *testing.T) {
	srv := newTestServerAsAdmin(t)
	skillID := srv.createSkill(t, "deploy-helper")
	ref := srv.pushSuite(t, "deploy-helper")
	jobID := srv.submitJob(t, skillID, "deploy-helper", 2, ref)
	claim := srv.postJSON(t, "/api/eval/jobs/claim", map[string]any{"worker_id": "w1", "lease_seconds": 600})
	token := claimToken(t, claim)

	before := time.Now()
	// Zero finished_at: an omitted field would marshal as the Go zero value,
	// 0001-01-01. If the server trusted it, this score would sort before
	// every other score forever.
	resp := srv.postReport(t, jobID, token, reportFixtureWith("deploy-helper", ref, 42, "0.9.1", time.Time{}))
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", resp.Code, resp.Body)
	}
	after := time.Now()

	q := quality.NewStore(srv.pool)
	rec, err := q.Latest(context.Background(), skillID, 2)
	if err != nil || rec == nil {
		t.Fatalf("no quality row was written: %v", err)
	}
	if rec.ScoredAt.Before(before) || rec.ScoredAt.After(after) {
		t.Fatalf("scored_at = %v, want between %v and %v (server clock, not the report's zero finished_at)", rec.ScoredAt, before, after)
	}
}

func TestCancelRoute_RequiresPrivilege(t *testing.T) {
	srv := newTestServer(t) // authenticated as a plain member
	skillID := srv.createSkill(t, "deploy-helper")
	jobID := srv.submitJob(t, skillID, "deploy-helper", 2, "sha256:abc")

	resp := srv.postJSON(t, "/api/eval/jobs/"+jobID+"/cancel", nil)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 — a member must not be able to cancel a job", resp.Code)
	}
}

func TestCancelRoute_CancelsAQueuedJob(t *testing.T) {
	srv := newTestServerAsAdmin(t)
	skillID := srv.createSkill(t, "deploy-helper")
	jobID := srv.submitJob(t, skillID, "deploy-helper", 2, "sha256:abc")

	resp := srv.postJSON(t, "/api/eval/jobs/"+jobID+"/cancel", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", resp.Code, resp.Body)
	}
	job := srv.getJob(t, jobID)
	if job.Status != "cancelled" {
		t.Fatalf("job status = %q, want cancelled", job.Status)
	}
}

func TestCancelRoute_409OnAFinishedJob(t *testing.T) {
	srv := newTestServerAsAdmin(t)
	skillID := srv.createSkill(t, "deploy-helper")
	ref := srv.pushSuite(t, "deploy-helper")
	jobID := srv.submitJob(t, skillID, "deploy-helper", 2, ref)
	claim := srv.postJSON(t, "/api/eval/jobs/claim", map[string]any{"worker_id": "w1", "lease_seconds": 600})
	token := claimToken(t, claim)

	resp := srv.postReport(t, jobID, token, reportFixture("deploy-helper", ref, 50))
	if resp.Code != http.StatusOK {
		t.Fatalf("setup: report failed: %d: %s", resp.Code, resp.Body)
	}

	cancel := srv.postJSON(t, "/api/eval/jobs/"+jobID+"/cancel", nil)
	if cancel.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 — a done job is not cancellable: %s", cancel.Code, cancel.Body)
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

// TestFailRoute_StoresThePlainLanguageLeadAndTheRawChain proves the
// Explain wiring in the fail handler (routes.go) actually reaches the
// stored job, not just Explain in isolation: a recognised raw error chain
// must persist as last_error with the plain-language sentence first and the
// original chain still present, since the handler concatenates them for a
// single column with no separate plain-language field.
func TestFailRoute_StoresThePlainLanguageLeadAndTheRawChain(t *testing.T) {
	srv := newTestServerAsAdmin(t)
	skillID := srv.createSkill(t, "deploy-helper")
	jobID := srv.submitJob(t, skillID, "deploy-helper", 2, "sha256:abc")
	claim := srv.postJSON(t, "/api/eval/jobs/claim", map[string]any{"worker_id": "w1", "lease_seconds": 600})
	token := claimToken(t, claim)

	// Reused from failure_test.go's "suite too thin" case, which Explain
	// recognises.
	raw := "worker: derive suite for x: derive: the derived suite is too thin to evaluate: runner: tier full needs 7 dev tasks, the suite has 3"
	body, err := json.Marshal(map[string]string{"error": raw})
	if err != nil {
		t.Fatalf("marshal fail body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/eval/jobs/"+jobID+"/fail", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Claim-Token", token)
	rr := httptest.NewRecorder()
	srv.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body)
	}

	job := srv.getJob(t, jobID)
	if !strings.HasPrefix(job.LastError, "This skill's evaluation suite had too few usable tasks to score") {
		t.Fatalf("last_error = %q, want it to lead with the plain-language sentence", job.LastError)
	}
	if !strings.Contains(job.LastError, raw) {
		t.Fatalf("last_error = %q, want it to still contain the raw chain %q", job.LastError, raw)
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

// testQualityFloor is the floor the test server decides with.
const testQualityFloor = 60

// heldVersion creates a version held for review on an appealable finding, the
// way publish does when the scanner guesses from shape.
func (s *testServer) heldVersion(t *testing.T, skillID, name string) int {
	t.Helper()
	rep := scan.Report{Findings: []scan.Finding{{
		Rule:     "curl-pipe",
		Severity: "high",
		File:     "SKILL.md",
		Line:     4,
		Message:  "piping a download into a shell",
		Class:    string(scan.ClassExecution),
	}}}
	scanJSON, err := json.Marshal(rep)
	if err != nil {
		t.Fatalf("marshal scan report: %v", err)
	}
	v, err := s.skills.CreateVersion(t.Context(), skillID, "p/"+name, "c-"+name, "", "d", "c",
		json.RawMessage(`{}`), nil, scanJSON, "t",
		gate.Decision{Outcome: gate.NeedsReview, Reasons: []gate.Reason{{Rule: "curl-pipe", Class: "execution"}}})
	if err != nil {
		t.Fatalf("CreateVersion: %v", err)
	}
	return v.Version
}

// claimOnly claims the single queued job and returns its claim token.
func (s *testServer) claimOnly(t *testing.T, jobID string) string {
	t.Helper()
	claim := s.postJSON(t, "/api/eval/jobs/claim", map[string]any{"worker_id": "w1", "lease_seconds": 600})
	if claim.Code != http.StatusOK {
		t.Fatalf("claim: status = %d: %s", claim.Code, claim.Body)
	}
	var claimed struct {
		Job        struct{ ID string } `json:"job"`
		ClaimToken string              `json:"claim_token"`
	}
	if err := json.Unmarshal(claim.Body.Bytes(), &claimed); err != nil {
		t.Fatalf("claim: unmarshal: %v", err)
	}
	if claimed.Job.ID != jobID {
		t.Fatalf("claimed job = %q, want %q", claimed.Job.ID, jobID)
	}
	return claimed.ClaimToken
}

// TestReportRoute_ClearingScoreReleasesAHeldVersion is what makes the gate
// something other than a permanent hold: the report that measures the skill is
// also what lets it out.
func TestReportRoute_ClearingScoreReleasesAHeldVersion(t *testing.T) {
	srv := newTestServerAsAdmin(t)
	skillID := srv.createSkill(t, "deploy-helper")
	version := srv.heldVersion(t, skillID, "deploy-helper")
	ref := srv.pushSuite(t, "deploy-helper")
	jobID := srv.submitJob(t, skillID, "deploy-helper", version, ref)

	token := srv.claimOnly(t, jobID)
	resp := srv.postReport(t, jobID, token, reportFixture("deploy-helper", ref, 82))
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", resp.Code, resp.Body)
	}

	ver, err := srv.skills.GetVersion(t.Context(), "deploy-helper", version)
	if err != nil || ver == nil {
		t.Fatalf("GetVersion: %v", err)
	}
	if ver.GateState != "released" {
		t.Fatalf("gate_state = %q, want released — a verified clearing score must release the version", ver.GateState)
	}
	sk, err := srv.skills.GetByName(t.Context(), "deploy-helper")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if sk.LatestVersion != version {
		t.Fatalf("latest_version = %d, want %d — a released version must be served", sk.LatestVersion, version)
	}
}

func TestListSkillEvals_ReturnsJobsNewestFirst(t *testing.T) {
	srv := newTestServerAsAdmin(t)
	skillID := srv.createSkill(t, "eval-list")
	older := srv.submitJob(t, skillID, "eval-list", 1, "sha256:abc")
	newer := srv.submitJob(t, skillID, "eval-list", 2, "sha256:abc")

	req := httptest.NewRequest(http.MethodGet, "/api/skills/eval-list/evals", nil)
	rr := httptest.NewRecorder()
	srv.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	var body struct {
		Jobs []struct {
			ID            string `json:"id"`
			QueuePosition int    `json:"queue_position"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Jobs) != 2 {
		t.Fatalf("jobs = %d, want 2", len(body.Jobs))
	}
	if body.Jobs[0].ID != newer || body.Jobs[1].ID != older {
		t.Fatalf("jobs out of order: %+v", body.Jobs)
	}
	if body.Jobs[1].QueuePosition != 0 || body.Jobs[0].QueuePosition != 1 {
		t.Fatalf("queue positions = %+v, want the older job first in line", body.Jobs)
	}
}

func TestListSkillEvals_UnknownSkillIs404(t *testing.T) {
	srv := newTestServerAsAdmin(t)
	req := httptest.NewRequest(http.MethodGet, "/api/skills/nope/evals", nil)
	rr := httptest.NewRecorder()
	srv.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

// TestReportRoute_ShortScoreLeavesTheVersionHeld is the other half: the score
// is stored, the job completes, and the version stays out of circulation.
func TestReportRoute_ShortScoreLeavesTheVersionHeld(t *testing.T) {
	srv := newTestServerAsAdmin(t)
	skillID := srv.createSkill(t, "deploy-helper")
	version := srv.heldVersion(t, skillID, "deploy-helper")
	ref := srv.pushSuite(t, "deploy-helper")
	jobID := srv.submitJob(t, skillID, "deploy-helper", version, ref)

	token := srv.claimOnly(t, jobID)
	resp := srv.postReport(t, jobID, token, reportFixture("deploy-helper", ref, 40))
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", resp.Code, resp.Body)
	}

	ver, err := srv.skills.GetVersion(t.Context(), "deploy-helper", version)
	if err != nil || ver == nil {
		t.Fatalf("GetVersion: %v", err)
	}
	if ver.GateState != "needs_review" {
		t.Fatalf("gate_state = %q, want needs_review — a score under the floor must not release", ver.GateState)
	}

	// The measurement is still worth keeping, and the job is still done.
	q := quality.NewStore(srv.pool)
	rec, err := q.Latest(context.Background(), skillID, version)
	if err != nil || rec == nil {
		t.Fatalf("a short score must still be stored: %v", err)
	}
	if job := srv.getJob(t, jobID); job.Status != "done" {
		t.Fatalf("job status = %q, want done", job.Status)
	}
}

// reportEnv is the fixture for the derive-suite report tests: a running
// server, so a test can push suites through its registry and inspect their
// origin the way the report handler does.
type reportEnv struct {
	*testServer
	skills map[string]string // skill name -> id, created on first use
}

func newReportEnv(t *testing.T) *reportEnv {
	t.Helper()
	return &reportEnv{testServer: newTestServerAsAdmin(t), skills: map[string]string{}}
}

func (e *reportEnv) skillID(t *testing.T, skillName string) string {
	t.Helper()
	if id, ok := e.skills[skillName]; ok {
		return id
	}
	id := e.createSkill(t, skillName)
	e.skills[skillName] = id
	return id
}

// claimedJob is a job already claimed by a fake worker, ready to report
// against.
type claimedJob struct {
	id    string
	token string
}

type jobOpt func(*evalqueue.Job)

// withSuiteRef sets the ref a job is submitted with; "" is a derive job.
func withSuiteRef(ref string) jobOpt {
	return func(j *evalqueue.Job) { j.SuiteRef = ref }
}

func (e *reportEnv) enqueueJob(t *testing.T, skillName string, opts ...jobOpt) *claimedJob {
	t.Helper()
	j := evalqueue.Job{SkillID: e.skillID(t, skillName), SkillName: skillName, Version: 1}
	for _, opt := range opts {
		opt(&j)
	}
	id, err := e.queue.Submit(context.Background(), j)
	if err != nil {
		t.Fatalf("enqueueJob: Submit: %v", err)
	}
	return &claimedJob{id: string(id), token: e.claimOnly(t, string(id))}
}

func (e *reportEnv) markDerived(t *testing.T, ref string) {
	t.Helper()
	if err := e.suites.MarkDerived(context.Background(), e.pool, ref); err != nil {
		t.Fatalf("markDerived: %v", err)
	}
}

func (e *reportEnv) report(t *testing.T, job *claimedJob, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	return e.postReport(t, job.id, job.token, body)
}

func (e *reportEnv) latestQuality(t *testing.T, skillName string, version int) *quality.Record {
	t.Helper()
	q := quality.NewStore(e.pool)
	rec, err := q.Latest(context.Background(), e.skillID(t, skillName), version)
	if err != nil {
		t.Fatalf("latestQuality: %v", err)
	}
	if rec == nil {
		t.Fatalf("latestQuality: no row for %s v%d", skillName, version)
	}
	return rec
}

// reportJSON builds a report body naming suiteRef, above the test floor so
// clearing behavior never confounds these tests.
func reportJSON(t *testing.T, skillName, suiteRef string) []byte {
	t.Helper()
	return reportFixture(skillName, suiteRef, 80)
}

func TestReport_AcceptsTheDerivedRefAndStampsOrigin(t *testing.T) {
	env := newReportEnv(t)
	job := env.enqueueJob(t, "demo", withSuiteRef(""))
	ref := env.pushSuite(t, "demo")

	res := env.report(t, job, reportJSON(t, "demo", ref))
	if res.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", res.Code, res.Body)
	}

	rec, err := env.suites.Get(context.Background(), ref)
	if err != nil {
		t.Fatalf("Get suite: %v", err)
	}
	if rec.Origin != evalsuite.OriginDerived {
		t.Fatalf("origin = %q, want derived", rec.Origin)
	}
}

func TestReport_RejectsARefBelongingToAnotherSkill(t *testing.T) {
	// eval_jobs.suite_ref carries no foreign key and refs are globally
	// unique, so an unchecked ref would attribute a score computed against
	// another skill's tasks and verifiers to this one.
	env := newReportEnv(t)
	job := env.enqueueJob(t, "demo", withSuiteRef(""))
	otherRef := env.pushSuite(t, "other-skill")

	res := env.report(t, job, reportJSON(t, "demo", otherRef))
	if res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422 for a ref belonging to another skill", res.Code)
	}
}

func TestReport_RejectsARefThatDoesNotExist(t *testing.T) {
	env := newReportEnv(t)
	job := env.enqueueJob(t, "demo", withSuiteRef(""))

	res := env.report(t, job, reportJSON(t, "demo", "no-such-ref"))
	if res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422 for an unknown ref", res.Code)
	}
}

func TestReport_StillRejectsAMismatchOnANamedJob(t *testing.T) {
	// The existing invariant is unchanged for a job that named its suite.
	env := newReportEnv(t)
	ref := env.pushSuite(t, "demo")
	job := env.enqueueJob(t, "demo", withSuiteRef(ref))

	res := env.report(t, job, reportJSON(t, "demo", "something-else"))
	if res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422", res.Code)
	}
}

// A job whose named suite is no longer registered is bad input, not a server
// fault: the report names a ref this server cannot resolve, which is the same
// condition the empty-ref branch already answers with 422.
func TestReport_UnregisteredRefOnANamedJobIs422(t *testing.T) {
	env := newReportEnv(t)
	ref := env.pushSuite(t, "demo")
	job := env.enqueueJob(t, "demo", withSuiteRef(ref))
	if _, err := env.pool.Exec(context.Background(), `DELETE FROM eval_suites WHERE ref = $1`, ref); err != nil {
		t.Fatalf("delete suite row: %v", err)
	}

	res := env.report(t, job, reportJSON(t, "demo", ref))
	if res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422 naming the missing ref: %s", res.Code, res.Body)
	}
	if !strings.Contains(res.Body.String(), ref) {
		t.Fatalf("the 422 does not name the missing ref: %s", res.Body)
	}
}

func TestReport_RecordsSuiteDerivedOnTheQualityRow(t *testing.T) {
	env := newReportEnv(t)
	job := env.enqueueJob(t, "demo", withSuiteRef(""))
	ref := env.pushSuite(t, "demo")

	env.report(t, job, reportJSON(t, "demo", ref))

	rec := env.latestQuality(t, "demo", 1)
	if !rec.SuiteDerived {
		t.Fatal("quality row does not record that the suite was derived")
	}
}

func TestReport_SecondRunAgainstAStoredDerivedSuiteIsStillDerived(t *testing.T) {
	// The job now names a ref, so "job.SuiteRef == ''" alone would call this
	// authored. The suite's own origin is what carries it forward.
	env := newReportEnv(t)
	ref := env.pushSuite(t, "demo")
	env.markDerived(t, ref)
	job := env.enqueueJob(t, "demo", withSuiteRef(ref))

	env.report(t, job, reportJSON(t, "demo", ref))

	rec := env.latestQuality(t, "demo", 1)
	if !rec.SuiteDerived {
		t.Fatal("a re-run against a stored derived suite was recorded as authored")
	}
}

// The tier is validated before any lookup, so a bad one needs no fixture: an
// unknown tier is bad input whether or not the skill exists.
func TestRerunEval_RejectsAnUnknownTier(t *testing.T) {
	srv := newTestServerAsAdmin(t)
	srv.createSkill(t, "demo")

	rr := srv.postJSON(t, "/api/skills/demo/evals", map[string]any{"tier": "banana"})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422 — an unknown tier reached the queue: %s", rr.Code, rr.Body.String())
	}
}

func TestRerunEval_RejectsAnUnknownTierBeforeTouchingTheSkill(t *testing.T) {
	srv := newTestServerAsAdmin(t)

	// No skill created: bad input must not depend on a database round-trip.
	rr := srv.postJSON(t, "/api/skills/nope/evals", map[string]any{"tier": "banana"})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422 (not 404) — the tier is validated first", rr.Code)
	}
}

// Every known tier must pass validation. These still 404 on the missing
// published version, which proves they got past the tier check — a 422 here
// would mean a valid tier was rejected.
func TestRerunEval_AcceptsEveryKnownTier(t *testing.T) {
	for _, tier := range []string{"smoke", "full", "deep"} {
		t.Run(tier, func(t *testing.T) {
			srv := newTestServerAsAdmin(t)
			srv.createSkill(t, "demo")

			rr := srv.postJSON(t, "/api/skills/demo/evals", map[string]any{"tier": tier})
			if rr.Code == http.StatusUnprocessableEntity {
				t.Errorf("tier %s was rejected as unknown: %s", tier, rr.Body.String())
			}
		})
	}
}

// Omitting the tier must keep meaning "full". Validating with a Huma enum tag
// instead of by hand would make the omitted value invalid and break this.
func TestRerunEval_OmittedTierIsAccepted(t *testing.T) {
	srv := newTestServerAsAdmin(t)
	srv.createSkill(t, "demo")

	rr := srv.postJSON(t, "/api/skills/demo/evals", map[string]any{})
	if rr.Code == http.StatusUnprocessableEntity {
		t.Errorf("an omitted tier was rejected: %s", rr.Body.String())
	}
}
