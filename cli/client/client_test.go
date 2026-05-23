package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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

	ver, scanBody, err := c.PublishVersion("my-skill", []byte("fake-archive"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if scanBody != nil {
		t.Errorf("expected nil scanBody on success, got: %s", scanBody)
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
// a non-nil error and a non-nil scanBody carrying the scan report.
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

	ver, scanBody, err := c.PublishVersion("bad-skill", []byte("malicious-archive"))
	if err == nil {
		t.Fatal("expected error for 422")
	}
	if ver != nil {
		t.Errorf("expected nil version on scan block, got: %+v", ver)
	}
	if scanBody == nil {
		t.Error("expected non-nil scanBody on scan block")
	}
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
