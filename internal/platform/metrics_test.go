package platform

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMetricsMiddleware_PassThrough(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})

	mw := MetricsMiddleware(inner)
	req := httptest.NewRequest(http.MethodPost, "/api/skills/foo", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d", rec.Code)
	}
}

func TestMetricsMiddleware_DefaultStatus(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// no explicit WriteHeader — should default to 200
		_, _ = w.Write([]byte("ok"))
	})

	mw := MetricsMiddleware(inner)
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestNormalizePath(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"/api/skills/my-skill", "/api/skills/:name"},
		{"/api/skills/my-skill/versions/3", "/api/skills/:name/versions/:version"},
		{"/api/health", "/api/health"},
		{"/api/skills", "/api/skills"},
		{"/metrics", "/metrics"},
		{"/api/skills/foo/bar", "/api/skills/:name/bar"},
	}

	for _, c := range cases {
		got := normalizePath(c.input)
		if got != c.want {
			t.Errorf("normalizePath(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}
