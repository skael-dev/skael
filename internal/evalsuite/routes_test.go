package evalsuite_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"

	"github.com/skael-dev/skael/internal/auth"
	"github.com/skael-dev/skael/internal/eval/suite"
	"github.com/skael-dev/skael/internal/evalsuite"
	"github.com/skael-dev/skael/internal/platform"
	"github.com/skael-dev/skael/internal/skill"
	"github.com/skael-dev/skael/internal/testutil"
)

// flakyStorage wraps a real platform.Storage but can be told to fail Write
// or Read unconditionally, so tests can force the infrastructure-failure
// branches (500) that a real disk-full or object-store outage would hit —
// without actually breaking the test's storage backend.
type flakyStorage struct {
	platform.Storage
	failWrite bool
	failRead  bool
}

func (f *flakyStorage) Write(ctx context.Context, name string, r io.Reader) (string, error) {
	if f.failWrite {
		return "", errors.New("flakyStorage: simulated write failure")
	}
	return f.Storage.Write(ctx, name, r)
}

func (f *flakyStorage) Read(ctx context.Context, name string) (io.ReadCloser, error) {
	if f.failRead {
		return nil, errors.New("flakyStorage: simulated read failure")
	}
	return f.Storage.Read(ctx, name)
}

// testServer wires the real router (auth context attached, evalsuite routes
// registered) for HTTP-level tests.
type testServer struct {
	handler http.Handler
	skills  *skill.Store
	reg     *evalsuite.Registry
	claims  *fakeClaims
	// user is the authenticated caller the fake middleware attaches. A test
	// changes its Role in place to act as an owner, because review-eval-suite
	// is owner or admin only.
	user *auth.User
}

// fakeClaims stands in for internal/evalqueue's PoolExecutor: it answers
// whether a push's job claim verifies, and records the ref the route asked it
// to write onto the job — the same write the real implementation makes inside
// the registry's transaction.
type fakeClaims struct {
	ok        bool
	verifyErr error
	recordErr error

	gotJobID, gotToken, gotSkill string
	recordedJobID, recordedRef   string
}

func (f *fakeClaims) VerifyDerivePush(_ context.Context, jobID, token, skillName string) (bool, error) {
	f.gotJobID, f.gotToken, f.gotSkill = jobID, token, skillName
	return f.ok, f.verifyErr
}

func (f *fakeClaims) RecordDerivedSuite(_ context.Context, _ evalsuite.Queryer, jobID, ref string) error {
	f.recordedJobID, f.recordedRef = jobID, ref
	return f.recordErr
}

// newTestServer spins up a fresh migrated Postgres testcontainer, local
// storage, and a router with the eval suite routes mounted behind a fake auth
// middleware that attaches an authenticated member to every request.
func newTestServer(t *testing.T) *testServer {
	t.Helper()
	storage, err := platform.NewLocalStorage(t.TempDir())
	if err != nil {
		t.Fatalf("newTestServer: storage: %v", err)
	}
	return newTestServerWithStorage(t, storage)
}

// newTestServerWithStorage is newTestServer but with caller-supplied storage,
// so tests can inject a flakyStorage to exercise the infrastructure-failure
// (500) branches without a real disk-full or object-store outage.
func newTestServerWithStorage(t *testing.T, storage platform.Storage) *testServer {
	t.Helper()

	pool := testutil.SetupTestDB(t)

	skillStore := skill.NewStore(pool)
	reg := evalsuite.NewRegistry(pool, storage)

	member := &auth.User{
		ID:    "00000000-0000-0000-0000-0000000000aa",
		Email: "member@example.com",
		Role:  auth.RoleMember,
	}

	r := chi.NewMux()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			req = req.WithContext(auth.ContextWithUser(req.Context(), member))
			next.ServeHTTP(w, req)
		})
	})
	api := humachi.New(r, huma.DefaultConfig("Test API", "1.0.0"))
	claims := &fakeClaims{}
	evalsuite.RegisterRoutes(api, r, reg, skillStore, evalsuite.RouteOptions{Claims: claims})

	return &testServer{handler: r, skills: skillStore, reg: reg, claims: claims, user: member}
}

// newUnauthTestServer wires the eval suite routes behind the real
// auth.Middleware (no session manager, no key store — every request is
// unauthenticated), so a test can prove the routes are not accidentally
// reachable without credentials.
func newUnauthTestServer(t *testing.T) *testServer {
	t.Helper()

	pool := testutil.SetupTestDB(t)
	storage, err := platform.NewLocalStorage(t.TempDir())
	if err != nil {
		t.Fatalf("newUnauthTestServer: storage: %v", err)
	}

	skillStore := skill.NewStore(pool)
	reg := evalsuite.NewRegistry(pool, storage)

	r := chi.NewMux()
	r.Use(auth.Middleware(nil, nil, nil))
	api := humachi.New(r, huma.DefaultConfig("Test API", "1.0.0"))
	evalsuite.RegisterRoutes(api, r, reg, skillStore, evalsuite.RouteOptions{})

	return &testServer{handler: r, skills: skillStore, reg: reg}
}

// createSkill registers a bare skill row so the upload handler's skill lookup
// succeeds.
func (s *testServer) createSkill(t *testing.T, name string) {
	t.Helper()
	if _, err := s.skills.Create(t.Context(), name, name, "test skill", "# "+name, json.RawMessage(`{}`)); err != nil {
		t.Fatalf("createSkill(%s): %v", name, err)
	}
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

func (s *testServer) get(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rr := httptest.NewRecorder()
	s.handler.ServeHTTP(rr, req)
	return rr
}

func TestPostSuite_StoresAndReturnsRef(t *testing.T) {
	srv := newTestServer(t)
	srv.createSkill(t, "deploy-helper")

	body := map[string]any{
		"skill":          "deploy-helper",
		"spec_version":   1,
		"checks":         []map[string]any{{"task_id": "t1", "ok": true}},
		"archive_base64": base64.StdEncoding.EncodeToString(fixtureSuiteArchive(t)),
	}
	resp := srv.postJSON(t, "/api/eval/suites", body)
	if resp.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", resp.Code, resp.Body)
	}
	var out struct {
		Ref       string `json:"ref"`
		TaskCount int    `json:"task_count"`
	}
	_ = json.Unmarshal(resp.Body.Bytes(), &out)
	if out.Ref == "" || out.TaskCount == 0 {
		t.Fatalf("response missing ref/task_count: %s", resp.Body)
	}

	dl := srv.get(t, "/api/eval/suites/"+url.PathEscape(out.Ref))
	if dl.Code != http.StatusOK {
		t.Fatalf("download status = %d, want 200", dl.Code)
	}
	if ct := dl.Header().Get("Content-Type"); ct != "application/gzip" {
		t.Fatalf("content type = %q", ct)
	}
}

// A push that presents a verified job claim is the eval worker deriving a
// suite for a skill that has none. Recording that at push time is what stops a
// run that never reports from leaving a machine-generated suite recorded as
// authored — which a later re-run's score could then use to clear a scan hold.
func TestPostSuite_AVerifiedJobClaimRecordsTheSuiteAsDerived(t *testing.T) {
	srv := newTestServer(t)
	srv.createSkill(t, "deploy-helper")
	srv.claims.ok = true

	body := map[string]any{
		"skill":          "deploy-helper",
		"spec_version":   1,
		"checks":         []map[string]any{{"task_id": "t1", "ok": true}},
		"archive_base64": base64.StdEncoding.EncodeToString(fixtureSuiteArchive(t)),
		"job_id":         "job-7",
		"claim_token":    "tok",
	}
	resp := srv.postJSON(t, "/api/eval/suites", body)
	if resp.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", resp.Code, resp.Body)
	}
	var out struct {
		Ref string `json:"ref"`
	}
	_ = json.Unmarshal(resp.Body.Bytes(), &out)

	rec, err := srv.reg.Get(t.Context(), out.Ref)
	if err != nil {
		t.Fatalf("Get(%s): %v", out.Ref, err)
	}
	if rec.Origin != evalsuite.OriginDerived {
		t.Fatalf("origin = %q immediately after the push, want %q — a suite that only becomes derived when a report lands is authored for as long as the run takes, and forever if it never reports",
			rec.Origin, evalsuite.OriginDerived)
	}
	if srv.claims.gotJobID != "job-7" || srv.claims.gotToken != "tok" || srv.claims.gotSkill != "deploy-helper" {
		t.Fatalf("claim verified with (%q, %q, %q)", srv.claims.gotJobID, srv.claims.gotToken, srv.claims.gotSkill)
	}
	if srv.claims.recordedJobID != "job-7" || srv.claims.recordedRef != out.Ref {
		t.Fatalf("job suite_ref recorded as (%q, %q), want (job-7, %q)", srv.claims.recordedJobID, srv.claims.recordedRef, out.Ref)
	}
}

// The claim is verified, never believed: a push cannot talk the server into
// attributing a suite to a job it does not hold — and equally cannot get its
// suite recorded authored by presenting a bad claim.
func TestPostSuite_AnUnverifiedJobClaimIsRejected(t *testing.T) {
	srv := newTestServer(t)
	srv.createSkill(t, "deploy-helper")
	srv.claims.ok = false

	body := map[string]any{
		"skill":          "deploy-helper",
		"spec_version":   1,
		"checks":         []map[string]any{{"task_id": "t1", "ok": true}},
		"archive_base64": base64.StdEncoding.EncodeToString(fixtureSuiteArchive(t)),
		"job_id":         "job-7",
		"claim_token":    "forged",
	}
	resp := srv.postJSON(t, "/api/eval/suites", body)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", resp.Code, resp.Body)
	}
	if srv.claims.recordedRef != "" {
		t.Fatalf("a rejected push still recorded a suite ref on the job: %q", srv.claims.recordedRef)
	}
}

// A push naming no job is the ordinary authored path (whetstone suite push),
// which must keep working exactly as before.
func TestPostSuite_APushWithNoJobStaysAuthored(t *testing.T) {
	srv := newTestServer(t)
	srv.createSkill(t, "deploy-helper")
	srv.claims.ok = true // would say yes if it were ever asked

	body := map[string]any{
		"skill":          "deploy-helper",
		"spec_version":   1,
		"checks":         []map[string]any{{"task_id": "t1", "ok": true}},
		"archive_base64": base64.StdEncoding.EncodeToString(fixtureSuiteArchive(t)),
	}
	resp := srv.postJSON(t, "/api/eval/suites", body)
	if resp.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", resp.Code, resp.Body)
	}
	var out struct {
		Ref string `json:"ref"`
	}
	_ = json.Unmarshal(resp.Body.Bytes(), &out)
	rec, err := srv.reg.Get(t.Context(), out.Ref)
	if err != nil {
		t.Fatalf("Get(%s): %v", out.Ref, err)
	}
	if rec.Origin != evalsuite.OriginAuthored {
		t.Fatalf("origin = %q, want %q", rec.Origin, evalsuite.OriginAuthored)
	}
	if srv.claims.gotJobID != "" {
		t.Fatalf("an authored push consulted the claim verifier with job %q", srv.claims.gotJobID)
	}
}

// TestPostSuite_AnUnreviewedPushIsRecordedDerived pins the meaning origin now
// carries. A suite the generator wrote and nobody read must not clear a scan
// hold. Origin is the one flag that says so.
//
// The claim path proves a worker derived the suite. This proves nobody read
// it. Both end at PutDerived, and no path lets a client claim the stronger
// status.
func TestPostSuite_AnUnreviewedPushIsRecordedDerived(t *testing.T) {
	srv := newTestServer(t)
	srv.createSkill(t, "deploy-helper")

	body := map[string]any{
		"skill":          "deploy-helper",
		"spec_version":   1,
		"checks":         []map[string]any{{"task_id": "t1", "ok": true}},
		"archive_base64": base64.StdEncoding.EncodeToString(fixtureSuiteArchive(t)),
		"unreviewed":     true,
	}
	resp := srv.postJSON(t, "/api/eval/suites", body)
	if resp.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", resp.Code, resp.Body)
	}
	var out struct {
		Ref string `json:"ref"`
	}
	_ = json.Unmarshal(resp.Body.Bytes(), &out)

	rec, err := srv.reg.Get(t.Context(), out.Ref)
	if err != nil {
		t.Fatalf("Get(%s): %v", out.Ref, err)
	}
	if rec.Origin != evalsuite.OriginDerived {
		t.Fatalf("origin = %q, want %q", rec.Origin, evalsuite.OriginDerived)
	}
}

func TestPostSuite_UnknownSkillIs404(t *testing.T) {
	srv := newTestServer(t)

	body := map[string]any{
		"skill":          "no-such-skill",
		"spec_version":   1,
		"checks":         []map[string]any{{"task_id": "t1", "ok": true}},
		"archive_base64": base64.StdEncoding.EncodeToString(fixtureSuiteArchive(t)),
	}
	resp := srv.postJSON(t, "/api/eval/suites", body)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", resp.Code, resp.Body)
	}
}

func TestPostSuite_NoChecksIs422(t *testing.T) {
	srv := newTestServer(t)
	srv.createSkill(t, "deploy-helper")

	body := map[string]any{"skill": "deploy-helper", "spec_version": 1, "checks": []any{},
		"archive_base64": base64.StdEncoding.EncodeToString(fixtureSuiteArchive(t))}
	resp := srv.postJSON(t, "/api/eval/suites", body)
	if resp.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", resp.Code, resp.Body)
	}
}

func TestGetSuite_UnknownRefIs404(t *testing.T) {
	srv := newTestServer(t)

	resp := srv.get(t, "/api/eval/suites/does-not-exist")
	if resp.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", resp.Code, resp.Body)
	}
}

// TestPostSuite_StorageFailureIs500 proves a storage write failure (disk
// full, object store outage) is reported as a 500, not folded into the same
// 422 an unusable archive gets — the caller did nothing wrong here.
func TestPostSuite_StorageFailureIs500(t *testing.T) {
	local, err := platform.NewLocalStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStorage: %v", err)
	}
	srv := newTestServerWithStorage(t, &flakyStorage{Storage: local, failWrite: true})
	srv.createSkill(t, "deploy-helper")

	body := map[string]any{
		"skill":          "deploy-helper",
		"spec_version":   1,
		"checks":         []map[string]any{{"task_id": "t1", "ok": true}},
		"archive_base64": base64.StdEncoding.EncodeToString(fixtureSuiteArchive(t)),
	}
	resp := srv.postJSON(t, "/api/eval/suites", body)
	if resp.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", resp.Code, resp.Body)
	}
}

// TestGetSuite_StorageFailureIs500 proves a storage read failure on a suite
// that genuinely exists (the DB row is there, the object store fails) is a
// 500, distinguishable from an unknown ref (404).
func TestGetSuite_StorageFailureIs500(t *testing.T) {
	local, err := platform.NewLocalStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStorage: %v", err)
	}
	flaky := &flakyStorage{Storage: local}
	srv := newTestServerWithStorage(t, flaky)
	srv.createSkill(t, "deploy-helper")

	body := map[string]any{
		"skill":          "deploy-helper",
		"spec_version":   1,
		"checks":         []map[string]any{{"task_id": "t1", "ok": true}},
		"archive_base64": base64.StdEncoding.EncodeToString(fixtureSuiteArchive(t)),
	}
	resp := srv.postJSON(t, "/api/eval/suites", body)
	if resp.Code != http.StatusCreated {
		t.Fatalf("setup upload status = %d, want 201: %s", resp.Code, resp.Body)
	}
	var out struct {
		Ref string `json:"ref"`
	}
	_ = json.Unmarshal(resp.Body.Bytes(), &out)

	flaky.failRead = true
	dl := srv.get(t, "/api/eval/suites/"+url.PathEscape(out.Ref))
	if dl.Code != http.StatusInternalServerError {
		t.Fatalf("download status = %d, want 500: %s", dl.Code, dl.Body)
	}
}

// TestPostSuite_UnauthenticatedIsRejected proves the route is not reachable
// without credentials, so a future accidental addition of these paths to the
// auth middleware's skip-list fails this test instead of passing silently.
func TestPostSuite_UnauthenticatedIsRejected(t *testing.T) {
	srv := newUnauthTestServer(t)

	body := map[string]any{
		"skill":          "deploy-helper",
		"spec_version":   1,
		"checks":         []map[string]any{{"task_id": "t1", "ok": true}},
		"archive_base64": base64.StdEncoding.EncodeToString(fixtureSuiteArchive(t)),
	}
	resp := srv.postJSON(t, "/api/eval/suites", body)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401: %s", resp.Code, resp.Body)
	}
}

// setupReviewFixture pushes the two-query fixture suite as derived. It
// returns the server, its ref, and the queries the archive holds. Every
// review test starts from this fixture.
func setupReviewFixture(t *testing.T) (*testServer, string, []suite.TriggerQuery) {
	t.Helper()
	srv := newTestServer(t)
	srv.createSkill(t, "deploy-helper")

	body := map[string]any{
		"skill":          "deploy-helper",
		"spec_version":   1,
		"checks":         []map[string]any{{"task_id": "t1", "ok": true}},
		"archive_base64": base64.StdEncoding.EncodeToString(fixtureSuiteArchive(t)),
		"unreviewed":     true,
	}
	resp := srv.postJSON(t, "/api/eval/suites", body)
	if resp.Code != http.StatusCreated {
		t.Fatalf("setup upload status = %d, want 201: %s", resp.Code, resp.Body)
	}
	var out struct {
		Ref string `json:"ref"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &out); err != nil {
		t.Fatalf("setup unmarshal: %v", err)
	}

	// The push is a member's to make. The review is not, so every review test
	// acts as an owner from here on.
	srv.user.Role = auth.RoleOwner

	// The two queries writeFixtureSuite stores in evals/triggers.json.
	originalQueries := []suite.TriggerQuery{
		{Query: "do the thing", ShouldTrigger: true},
		{Query: "do something unrelated"},
	}
	return srv, out.Ref, originalQueries
}

// reviewResult is the review-eval-suite response body.
type reviewResult struct {
	Ref     string `json:"ref"`
	Changed bool   `json:"changed"`
}

// postReview posts a review of ref's trigger queries. It returns the
// decoded response.
func postReview(t *testing.T, srv *testServer, ref string, queries []suite.TriggerQuery) reviewResult {
	t.Helper()
	resp := srv.postJSON(t, "/api/eval/suites/"+ref+"/review", map[string]any{"triggers": queries})
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", resp.Code, resp.Body)
	}
	var out reviewResult
	if err := json.Unmarshal(resp.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

// TestGetTriggers_ServesTheStoredQuerySet is what pre-populates the review
// view. Without it the browser must unpack a tarball to read one small JSON
// file.
func TestGetTriggers_ServesTheStoredQuerySet(t *testing.T) {
	srv, ref, _ := setupReviewFixture(t)

	resp := srv.get(t, "/api/eval/suites/"+ref+"/triggers")
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", resp.Code, resp.Body)
	}
	var body struct {
		Triggers []struct {
			Query         string `json:"query"`
			ShouldTrigger bool   `json:"should_trigger"`
		} `json:"triggers"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Triggers) != 2 {
		t.Fatalf("served %d queries, want 2", len(body.Triggers))
	}
}

// TestReview_NoEditMarksTheSameRefAuthored is the case that clears a badge on
// an existing score. A person read the eval set. The person vouched for it.
// The score already measured against exactly these queries.
func TestReview_NoEditMarksTheSameRefAuthored(t *testing.T) {
	srv, ref, originalQueries := setupReviewFixture(t)

	out := postReview(t, srv, ref, originalQueries)

	if out.Ref != ref {
		t.Errorf("ref = %q, want the unchanged %q", out.Ref, ref)
	}
	if out.Changed {
		t.Error("changed = true for a review that edited nothing")
	}
	rec, err := srv.reg.Get(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Origin != evalsuite.OriginAuthored {
		t.Errorf("origin = %q, want authored", rec.Origin)
	}
	// uploaded_by still names whoever pushed the suite, so without this column
	// the database cannot say who vouched for it.
	if rec.ReviewedBy != srv.user.Email {
		t.Errorf("reviewed_by = %q, want the reviewer %q", rec.ReviewedBy, srv.user.Email)
	}
}

// TestReview_AMemberIsRefused is the gate. This route decides whether a score
// can release a version the publish gate holds, so it sits with
// claim-eval-job, cancel-eval-job and rerun-eval rather than with the reads.
func TestReview_AMemberIsRefused(t *testing.T) {
	srv, ref, originalQueries := setupReviewFixture(t)
	srv.user.Role = auth.RoleMember

	resp := srv.postJSON(t, "/api/eval/suites/"+ref+"/review", map[string]any{"triggers": originalQueries})
	if resp.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", resp.Code, resp.Body)
	}

	rec, err := srv.reg.Get(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Origin != evalsuite.OriginDerived {
		t.Errorf("origin = %q, want it left derived: a member raised a suite", rec.Origin)
	}
}

// TestReview_AnEditStoresANewAuthoredRefAndLeavesTheOldOne is the honest
// half. The existing score measured a different eval set, so its badge must
// stay.
func TestReview_AnEditStoresANewAuthoredRefAndLeavesTheOldOne(t *testing.T) {
	srv, ref, originalQueries := setupReviewFixture(t)
	edited := append(append([]suite.TriggerQuery(nil), originalQueries...),
		suite.TriggerQuery{Query: "one more query", ShouldTrigger: true})

	out := postReview(t, srv, ref, edited)

	if out.Ref == ref {
		t.Fatal("an edited review reused the old ref")
	}
	if !out.Changed {
		t.Error("changed = false for a review that added a query")
	}

	old, err := srv.reg.Get(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if old.Origin != evalsuite.OriginDerived {
		t.Errorf("the old ref is %q, want it left derived", old.Origin)
	}
	fresh, err := srv.reg.Get(ctx, out.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Origin != evalsuite.OriginAuthored {
		t.Errorf("the new ref is %q, want authored", fresh.Origin)
	}
	if fresh.ReviewedBy != srv.user.Email {
		t.Errorf("reviewed_by = %q, want the reviewer %q", fresh.ReviewedBy, srv.user.Email)
	}
}

// TestReview_UnknownRefIs404 is a review of a ref that does not exist. It
// must not create one.
func TestReview_UnknownRefIs404(t *testing.T) {
	srv := newTestServer(t)
	srv.user.Role = auth.RoleOwner

	resp := srv.postJSON(t, "/api/eval/suites/does-not-exist/review", map[string]any{
		"triggers": []suite.TriggerQuery{{Query: "q", ShouldTrigger: true}},
	})
	if resp.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", resp.Code, resp.Body)
	}

	_, err := srv.reg.Get(ctx, "does-not-exist")
	if !errors.Is(err, evalsuite.ErrNotFound) {
		t.Fatalf("Get(does-not-exist) err = %v, want ErrNotFound", err)
	}
}
