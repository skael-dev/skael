package evalsuite_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
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

	pool := testutil.SetupTestDB(t)
	storage, err := platform.NewLocalStorage(t.TempDir())
	if err != nil {
		t.Fatalf("newTestServer: storage: %v", err)
	}

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
