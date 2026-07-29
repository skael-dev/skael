package platform

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
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

// TestClassifiedRateLimiter_AuthClassIgnoresAPIKeyHeader is the regression
// test for the forged-key auth bypass: /api/auth/login is unauthenticated,
// so X-API-Key on it is unverified. An attacker rotating a fresh key on every
// attempt must not be able to mint a fresh budget each time — the auth class
// must be keyed on IP alone.
func TestClassifiedRateLimiter_AuthClassIgnoresAPIKeyHeader(t *testing.T) {
	mw := ClassifiedRateLimiter(RateLimitConfig{Auth: 5, Events: 1000, Read: 1000, Write: 1000})

	i := 0
	allowed := send(t, mw, 1000, func() *http.Request {
		i++
		r := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
		r.RemoteAddr = "10.0.0.1:5000"
		r.Header.Set("X-API-Key", fmt.Sprintf("sk-forged-%d", i))
		return r
	})

	assert.LessOrEqualf(t, allowed, 5, "a rotating forged API key must not escape the auth budget (got %d/1000 allowed)", allowed)
}

// TestClassifiedRateLimiter_IPCeilingCapsRotationButNotFixedKeys proves both
// halves of the two-bucket design hold at once: two real developers behind
// one NAT, identified by distinct fixed API keys, each keep their own full
// per-key budget; an attacker behind the same IP rotating a fresh key on
// every request is bounded by the shared IP ceiling regardless.
func TestClassifiedRateLimiter_IPCeilingCapsRotationButNotFixedKeys(t *testing.T) {
	// Read: 3 per subject: IP ceiling = 3 * ipCeilingFactor(10) = 30.
	mw := ClassifiedRateLimiter(RateLimitConfig{Auth: 20, Events: 20, Read: 3, Write: 20})
	const ip = "10.0.0.55:5000"

	build := func(key string) func() *http.Request {
		return func() *http.Request {
			r := httptest.NewRequest(http.MethodGet, "/api/skills", nil)
			r.RemoteAddr = ip
			r.Header.Set("X-API-Key", key)
			return r
		}
	}

	// Two fixed, distinct developer keys behind the same IP: each gets its
	// own independent per-key budget of 3, unaffected by the other. Every
	// attempt (allowed or not) is charged one IP-ceiling token, since the IP
	// bucket is checked before the per-key bucket denies.
	allowedA := send(t, mw, 5, build("sk-fixed-aaaaaaaaaaaa"))
	require.Equal(t, 3, allowedA, "first fixed key should get its full independent budget")

	allowedB := send(t, mw, 5, build("sk-fixed-bbbbbbbbbbbb"))
	require.Equal(t, 3, allowedB, "second fixed key behind the same IP keeps its own budget")

	// 10 IP-ceiling tokens have now been spent (5 attempts each, all charged
	// regardless of the per-key outcome), leaving 20 of the 30-token ceiling.
	// A key-rotating attacker behind the same IP never trips its own per-key
	// bucket (every key is brand new), so it is bounded purely by what's left
	// of the shared IP ceiling.
	i := 0
	allowedRotating := send(t, mw, 40, func() *http.Request {
		i++
		return build(fmt.Sprintf("sk-rotate-%d", i))()
	})
	assert.Equal(t, 20, allowedRotating, "the shared IP ceiling must bound a key-rotating attacker")
}

// TestClassifiedRateLimiter_ConcurrentAccess exercises the middleware from
// many goroutines across all four classes and a mix of fixed and rotating
// subjects, so `go test -race` can verify the locking discipline under
// contention rather than just in sequential tests.
func TestClassifiedRateLimiter_ConcurrentAccess(t *testing.T) {
	mw := ClassifiedRateLimiter(RateLimitConfig{Auth: 50, Events: 50, Read: 50, Write: 50})
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	requests := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/auth/login"},
		{http.MethodPost, "/api/events"},
		{http.MethodGet, "/api/skills"},
		{http.MethodPost, "/api/skills/demo/versions"},
	}

	const goroutines = 64
	const perGoroutine = 200

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				rc := requests[(g+i)%len(requests)]
				r := httptest.NewRequest(rc.method, rc.path, nil)
				r.RemoteAddr = fmt.Sprintf("10.0.%d.%d:5000", g%256, i%256)
				r.Header.Set("X-API-Key", fmt.Sprintf("sk-%d-%d", g, i%5))
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, r)
			}
		}(g)
	}
	wg.Wait()
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
