package auth_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/skael-dev/skael/internal/auth"
)

const testAPIKey = "test-api-key-12345"

// okHandler is a trivial handler that writes 200 OK.
var okHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
})

func TestMiddleware_MissingKey(t *testing.T) {
	handler := auth.Middleware(testAPIKey)(okHandler)

	req := httptest.NewRequest(http.MethodGet, "/api/skills", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}

	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("response body is not valid JSON: %v", err)
	}
	if _, ok := body["error"]; !ok {
		t.Error("expected JSON body to contain 'error' key")
	}
}

func TestMiddleware_InvalidKey(t *testing.T) {
	handler := auth.Middleware(testAPIKey)(okHandler)

	req := httptest.NewRequest(http.MethodGet, "/api/skills", nil)
	req.Header.Set("X-API-Key", "wrong-key")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}

	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("response body is not valid JSON: %v", err)
	}
	if _, ok := body["error"]; !ok {
		t.Error("expected JSON body to contain 'error' key")
	}
}

func TestMiddleware_ValidKey(t *testing.T) {
	handler := auth.Middleware(testAPIKey)(okHandler)

	req := httptest.NewRequest(http.MethodGet, "/api/skills", nil)
	req.Header.Set("X-API-Key", testAPIKey)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestMiddleware_SkipsHealth(t *testing.T) {
	handler := auth.Middleware(testAPIKey)(okHandler)

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	// No X-API-Key header intentionally
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for /api/health without key, got %d", rec.Code)
	}
}
