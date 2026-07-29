package client

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockServer starts a test HTTP server with the given handler and returns both
// the server and a Client pointed at it with api key "test-key".
func mockServer(handler http.HandlerFunc) (*httptest.Server, *Client) {
	srv := httptest.NewServer(handler)
	c := New(srv.URL, "test-key")
	return srv, c
}

// TestClient_Health_Success verifies that a 200 response from /api/health returns no error.
func TestClient_Health_Success(t *testing.T) {
	srv, c := mockServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/health" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	})
	defer srv.Close()

	if err := c.Health(); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

// TestClient_Health_ServerDown verifies that connecting to a port with no listener
// returns a non-nil error.
func TestClient_Health_ServerDown(t *testing.T) {
	// Port 1 is reserved and will always refuse connections.
	c := New("http://localhost:1", "test-key")
	if err := c.Health(); err == nil {
		t.Fatal("expected error when server is unreachable")
	}
}

// TestClient_ListSkills verifies that the response is parsed correctly and that
// the X-API-Key header is forwarded by the client.
func TestClient_ListSkills(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	srv, c := mockServer(func(w http.ResponseWriter, r *http.Request) {
		if apiKey := r.Header.Get("X-API-Key"); apiKey != "test-key" {
			t.Errorf("expected X-API-Key 'test-key', got %q", apiKey)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"skills": []map[string]interface{}{
				{
					"name":           "my-skill",
					"description":    "A test skill",
					"latest_version": 2,
					"created_at":     now.Format(time.RFC3339),
					"updated_at":     now.Format(time.RFC3339),
				},
			},
			"total": 1,
		})
	})
	defer srv.Close()

	skills, total, err := c.ListSkills(10, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 1 {
		t.Errorf("expected total 1, got %d", total)
	}
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(skills))
	}
	if skills[0].Name != "my-skill" {
		t.Errorf("expected skill name 'my-skill', got %q", skills[0].Name)
	}
	if skills[0].LatestVersion != 2 {
		t.Errorf("expected latest_version 2, got %d", skills[0].LatestVersion)
	}
}

// TestClient_GetSkill_NotFound verifies that a 404 from the server returns
// (nil, nil) — no skill and no error.
func TestClient_GetSkill_NotFound(t *testing.T) {
	srv, c := mockServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"detail": "skill not found"})
	})
	defer srv.Close()

	sk, err := c.GetSkill("nonexistent")
	if err != nil {
		t.Fatalf("expected nil error for 404, got: %v", err)
	}
	if sk != nil {
		t.Errorf("expected nil skill for 404, got: %+v", sk)
	}
}

// TestClient_PublishVersion_Success verifies that a 201 response is parsed into
// a Version struct correctly.
func TestClient_PublishVersion_Success(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	srv, c := mockServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"version":    3,
			"checksum":   "abc123def456",
			"changelog":  "initial release",
			"created_at": now.Format(time.RFC3339),
		})
	})
	defer srv.Close()

	ver, report, err := c.PublishVersion("my-skill", []byte("fake-archive"), false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report != nil {
		t.Errorf("expected nil report on success, got: %+v", report)
	}
	if ver == nil {
		t.Fatal("expected non-nil version")
	}
	if ver.Version != 3 {
		t.Errorf("expected version 3, got %d", ver.Version)
	}
	if ver.Checksum != "abc123def456" {
		t.Errorf("expected checksum 'abc123def456', got %q", ver.Checksum)
	}
}

// TestClient_PublishVersion_ScanBlocked verifies that a 422 response results in
// a non-nil error. The response here carries no recognizable scan report
// envelope, so the report should come back nil rather than error out.
func TestClient_PublishVersion_ScanBlocked(t *testing.T) {
	scanReport := map[string]interface{}{
		"blocked": true,
		"reason":  "malicious code detected",
	}
	srv, c := mockServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(scanReport)
	})
	defer srv.Close()

	ver, report, err := c.PublishVersion("bad-skill", []byte("malicious-archive"), false)
	if err == nil {
		t.Fatal("expected error for 422")
	}
	if ver != nil {
		t.Errorf("expected nil version on scan block, got: %+v", ver)
	}
	if report != nil {
		t.Errorf("expected nil report when the body carries no scan envelope, got: %+v", report)
	}
}

// TestPublishVersion_ReturnsServerScanReport verifies that PublishVersion
// forwards the override flag as a query parameter and, on a 422 rejection,
// decodes the server's scan report out of the Huma error envelope so the
// caller can show the findings that actually blocked the publish.
func TestPublishVersion_ReturnsServerScanReport(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "true", r.URL.Query().Get("override"),
			"the override flag must reach the server")

		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{
			"title": "Unprocessable Entity",
			"detail": "archive rejected: resolve the findings below, or have an owner or admin publish with override",
			"errors": [{"message": "{\"status\":\"warn\",\"findings\":[{\"rule\":\"pipe-to-shell\",\"severity\":\"high\",\"file\":\"SKILL.md\",\"line\":12,\"message\":\"pipes a downloaded script into a shell\"}],\"summary\":{\"critical\":0,\"high\":1,\"medium\":0,\"info\":0}}"}]
		}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "sk-test")

	ver, report, err := c.PublishVersion("demo", []byte("archive"), true)
	require.Error(t, err)
	assert.Nil(t, ver)

	require.NotNil(t, report, "the server's scan report must reach the caller")
	assert.Equal(t, "warn", report.Status)
	require.Len(t, report.Findings, 1)
	assert.Equal(t, "SKILL.md", report.Findings[0].File)
	assert.Equal(t, 12, report.Findings[0].Line)
	assert.Contains(t, report.Findings[0].Message, "pipes a downloaded script")
}

// TestClient_SearchSkills verifies that the query parameter is forwarded and
// the response is parsed into the returned slice.
func TestClient_SearchSkills(t *testing.T) {
	srv, c := mockServer(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		if q != "hello" {
			t.Errorf("expected query param q='hello', got %q", q)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"skills": []map[string]interface{}{
				{"name": "hello-skill", "description": "says hello"},
				{"name": "hello-world", "description": "classic"},
			},
		})
	})
	defer srv.Close()

	skills, err := c.SearchSkills("hello", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(skills) != 2 {
		t.Fatalf("expected 2 results, got %d", len(skills))
	}
	if skills[0].Name != "hello-skill" {
		t.Errorf("expected first skill 'hello-skill', got %q", skills[0].Name)
	}
}

// TestClient_GetSkill_ServerError verifies that a 500 response returns a
// non-nil error and nil skill.
func TestClient_GetSkill_ServerError(t *testing.T) {
	srv, c := mockServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"detail": "internal server error"})
	})
	defer srv.Close()

	sk, err := c.GetSkill("some-skill")
	if err == nil {
		t.Fatal("expected non-nil error for 500")
	}
	if sk != nil {
		t.Errorf("expected nil skill on server error, got: %+v", sk)
	}
}

// TestClient_CreateSkill_Success verifies that a 201 response is parsed into a
// Skill struct correctly.
func TestClient_CreateSkill_Success(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	srv, c := mockServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/api/skills" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"name":           "new-skill",
			"description":    "a brand new skill",
			"latest_version": 0,
			"created_at":     now.Format(time.RFC3339),
			"updated_at":     now.Format(time.RFC3339),
		})
	})
	defer srv.Close()

	sk, err := c.CreateSkill("new-skill", "a brand new skill")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sk == nil {
		t.Fatal("expected non-nil skill")
	}
	if sk.Name != "new-skill" {
		t.Errorf("expected name 'new-skill', got %q", sk.Name)
	}
	if sk.Description != "a brand new skill" {
		t.Errorf("expected description 'a brand new skill', got %q", sk.Description)
	}
}

// TestClient_CreateSkill_Conflict verifies that a 409 response returns a
// non-nil error.
func TestClient_CreateSkill_Conflict(t *testing.T) {
	srv, c := mockServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]string{"detail": "skill already exists"})
	})
	defer srv.Close()

	sk, err := c.CreateSkill("existing-skill", "duplicate")
	if err == nil {
		t.Fatal("expected error for 409 conflict")
	}
	if sk != nil {
		t.Errorf("expected nil skill on conflict, got: %+v", sk)
	}
}

// TestClient_DownloadVersion_Success verifies that a 200 response returns the
// raw archive bytes.
func TestClient_DownloadVersion_Success(t *testing.T) {
	fakeArchive := []byte("fake-gzip-archive-content")
	srv, c := mockServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/gzip")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(fakeArchive)
	})
	defer srv.Close()

	data, err := c.DownloadVersion("my-skill", 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != string(fakeArchive) {
		t.Errorf("expected %q, got %q", fakeArchive, data)
	}
}

// TestClient_DownloadVersion_NotFound verifies that a 404 response returns a
// non-nil error.
func TestClient_DownloadVersion_NotFound(t *testing.T) {
	srv, c := mockServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"detail": "version not found"})
	})
	defer srv.Close()

	data, err := c.DownloadVersion("ghost-skill", 99)
	if err == nil {
		t.Fatal("expected error for 404")
	}
	if data != nil {
		t.Errorf("expected nil data on 404, got %d bytes", len(data))
	}
}

// TestRetryOn503 verifies that doWithRetry retries on 503 responses and
// succeeds when the server eventually returns 200.
func TestRetryOn503(t *testing.T) {
	attempts := 0
	srv, c := mockServer(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	defer srv.Close()

	c.httpClient.Timeout = 5 * time.Second

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/health", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := c.doWithRetry(req)
	if err != nil {
		t.Fatalf("expected success after retries, got: %v", err)
	}
	resp.Body.Close()
	if attempts != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts)
	}
}

// TestNoRetryOn4xx verifies that doWithRetry does not retry on 4xx responses.
func TestNoRetryOn4xx(t *testing.T) {
	attempts := 0
	srv, c := mockServer(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusBadRequest)
	})
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/health", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := c.doWithRetry(req)
	if err != nil {
		t.Fatalf("expected non-retry response returned as-is, got: %v", err)
	}
	resp.Body.Close()
	if attempts != 1 {
		t.Errorf("expected exactly 1 attempt for 4xx, got %d", attempts)
	}
}

// TestRetryOnConnectionError verifies that doWithRetry retries on connection errors.
func TestRetryOnConnectionError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close()

	c := New(srv.URL, "test-key")
	c.httpClient.Timeout = 2 * time.Second

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/health", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	_, err = c.doWithRetry(req)
	if err == nil {
		t.Fatal("expected error when connecting to closed server")
	}
	if !strings.Contains(err.Error(), "after 4 attempts") {
		t.Errorf("expected 'after 4 attempts' in error, got: %v", err)
	}
}

// TestDoWithRetry_NonReplayableBody verifies that doWithRetry refuses to retry
// requests with non-replayable bodies (no GetBody callback). This guards against
// silently sending empty bodies when non-seekable readers are used.
func TestDoWithRetry_NonReplayableBody(t *testing.T) {
	var requestCount int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		// Always return 503 to trigger a retry
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := New(srv.URL, "test-key")

	// Create a request manually with a non-replayable body.
	// Create the request with a pipe (which has no Seek capability and no GetBody).
	pr, pw := io.Pipe()
	go func() {
		// The reader may close first; a short write here is expected, not a failure.
		_, _ = pw.Write([]byte("test-body"))
		pw.Close()
	}()

	req, err := http.NewRequest(http.MethodPost, srv.URL, pr)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	// Explicitly ensure GetBody is nil to simulate a non-replayable reader
	req.GetBody = nil

	// doWithRetry should error rather than silently retry with empty body
	resp, err := c.doWithRetry(req)
	if err == nil {
		t.Fatal("expected error when body is non-replayable, got nil")
	}
	if !strings.Contains(err.Error(), "cannot be replayed") {
		t.Errorf("expected error containing 'cannot be replayed', got: %v", err)
	}
	if resp != nil {
		resp.Body.Close()
	}

	// Verify server received only ONE request (not a retry with empty body)
	if requestCount != 1 {
		t.Errorf("expected server to receive 1 request, got %d", requestCount)
	}
}

// TestClient_GetManifest verifies that the manifest array is parsed and returns
// the expected number of entries.
func TestClient_GetManifest(t *testing.T) {
	srv, c := mockServer(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/sync/manifest") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]interface{}{
			{"name": "skill-a", "version": 1, "checksum": "aaa111"},
			{"name": "skill-b", "version": 5, "checksum": "bbb555"},
		})
	})
	defer srv.Close()

	entries, err := c.GetManifest()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Name != "skill-a" {
		t.Errorf("expected first entry 'skill-a', got %q", entries[0].Name)
	}
	if entries[1].Version != 5 {
		t.Errorf("expected second entry version 5, got %d", entries[1].Version)
	}
}

// TestDownloadVersion_RetriesOn429 verifies that a 429 response is retried
// after honouring the server's Retry-After header, rather than surfaced as a
// hard failure.
func TestDownloadVersion_RetriesOn429(t *testing.T) {
	var mu sync.Mutex
	calls := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()

		if n == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte("archive"))
	}))
	defer srv.Close()

	c := New(srv.URL, "sk-test")

	start := time.Now()
	data, err := c.DownloadVersion("demo", 1)
	if err != nil {
		t.Fatalf("429 must be retried, not surfaced as a hard failure: %v", err)
	}
	if string(data) != "archive" {
		t.Errorf("expected %q, got %q", "archive", data)
	}
	if elapsed := time.Since(start); elapsed < time.Second {
		t.Errorf("expected Retry-After: 1 to be honoured, only waited %v", elapsed)
	}

	mu.Lock()
	defer mu.Unlock()
	if calls != 2 {
		t.Errorf("expected 2 calls, got %d", calls)
	}
}

// TestDownloadVersion_GivesUpAfterPersistent429 verifies that doWithRetry
// eventually gives up and surfaces an error when the server never stops
// rate limiting.
func TestDownloadVersion_GivesUpAfterPersistent429(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	c := New(srv.URL, "sk-test")

	_, err := c.DownloadVersion("demo", 1)
	if err == nil {
		t.Fatal("expected error after persistent 429s")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("expected error to mention 429, got: %v", err)
	}
}
