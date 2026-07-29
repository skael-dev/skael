package platform

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClassifyRequest(t *testing.T) {
	cases := []struct {
		method string
		path   string
		want   rateLimitClass
	}{
		{http.MethodPost, "/api/auth/login", classAuth},
		{http.MethodPost, "/api/auth/signup", classAuth},
		{http.MethodPost, "/api/events", classEvents},
		{http.MethodGet, "/api/skills", classRead},
		{http.MethodGet, "/api/skills/demo/versions/1/download", classRead},
		{http.MethodGet, "/api/sync/manifest", classRead},
		{http.MethodPost, "/api/skills/demo/versions", classWrite},
		{http.MethodDelete, "/api/skills/demo", classWrite},
	}

	for _, tc := range cases {
		r := httptest.NewRequest(tc.method, tc.path, nil)
		assert.Equalf(t, tc.want, classifyRequest(r), "%s %s", tc.method, tc.path)
	}
}

func TestRateLimitSubject_DistinguishesKeysBehindOneIP(t *testing.T) {
	a := httptest.NewRequest(http.MethodGet, "/api/skills", nil)
	a.RemoteAddr = "10.0.0.1:5000"
	a.Header.Set("X-API-Key", "sk-aaaaaaaaaaaa")

	b := httptest.NewRequest(http.MethodGet, "/api/skills", nil)
	b.RemoteAddr = "10.0.0.1:5001"
	b.Header.Set("X-API-Key", "sk-bbbbbbbbbbbb")

	assert.NotEqual(t, rateLimitSubject(a), rateLimitSubject(b),
		"two developers behind one NAT must not share a budget")
	assert.NotContains(t, rateLimitSubject(a), "sk-aaaaaaaaaaaa",
		"the raw API key must not be used as a map key")

	anon := httptest.NewRequest(http.MethodGet, "/api/skills", nil)
	anon.RemoteAddr = "10.0.0.1:5002"
	assert.Equal(t, "ip:10.0.0.1", rateLimitSubject(anon))
}

// send fires n requests through mw and returns how many were allowed through.
func send(t *testing.T, mw func(http.Handler) http.Handler, n int, build func() *http.Request) int {
	t.Helper()
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	allowed := 0
	for i := 0; i < n; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, build())
		if rec.Code == http.StatusOK {
			allowed++
		}
	}
	return allowed
}

func TestClassifiedRateLimiter_ClassesHaveSeparateBudgets(t *testing.T) {
	mw := ClassifiedRateLimiter(RateLimitConfig{Auth: 2, Events: 50, Read: 50, Write: 50})

	allowedAuth := send(t, mw, 10, func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
		r.RemoteAddr = "10.0.0.1:5000"
		return r
	})
	assert.Equal(t, 2, allowedAuth, "auth budget must be strict")

	allowedEvents := send(t, mw, 40, func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/api/events", nil)
		r.RemoteAddr = "10.0.0.1:5000"
		return r
	})
	assert.Equal(t, 40, allowedEvents, "exhausting the auth budget must not block event ingestion")
}

func TestClassifiedRateLimiter_KeysAreIndependent(t *testing.T) {
	mw := ClassifiedRateLimiter(RateLimitConfig{Auth: 20, Events: 20, Read: 3, Write: 20})

	allowedA := send(t, mw, 10, func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/api/skills", nil)
		r.RemoteAddr = "10.0.0.1:5000"
		r.Header.Set("X-API-Key", "sk-aaaaaaaaaaaa")
		return r
	})
	require.Equal(t, 3, allowedA)

	allowedB := send(t, mw, 3, func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/api/skills", nil)
		r.RemoteAddr = "10.0.0.1:5000"
		r.Header.Set("X-API-Key", "sk-bbbbbbbbbbbb")
		return r
	})
	assert.Equal(t, 3, allowedB, "a second key behind the same IP keeps its own budget")
}

func TestClassifiedRateLimiter_SetsRetryAfter(t *testing.T) {
	mw := ClassifiedRateLimiter(RateLimitConfig{Auth: 1, Events: 1, Read: 1, Write: 1})
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	build := func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/api/skills", nil)
		r.RemoteAddr = "10.0.0.9:5000"
		return r
	}

	h.ServeHTTP(httptest.NewRecorder(), build())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, build())
	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	assert.NotEmpty(t, rec.Header().Get("Retry-After"), "429 must tell the client when to come back")
}
