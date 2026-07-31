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
	evalsuite.RegisterRoutes(api, r, reg, skillStore)

	return &testServer{handler: r, skills: skillStore}
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
	evalsuite.RegisterRoutes(api, r, reg, skillStore)

	return &testServer{handler: r, skills: skillStore}
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
