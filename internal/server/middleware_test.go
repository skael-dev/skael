package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skael-dev/skael/internal/platform"
)

// edgeChain builds the real edge middleware stack — the same call Build makes,
// in the same order — over a handler that answers 200 to anything. Requests
// never reach a database, so the whole chain can be measured directly.
func edgeChain(t *testing.T, cfg *platform.Config) http.Handler {
	t.Helper()

	// The request logger writes a line per request; these tests send thousands.
	prev := log.Logger
	log.Logger = zerolog.New(io.Discard)
	t.Cleanup(func() { log.Logger = prev })

	router := chi.NewMux()
	mountMetrics := InstallEdgeMiddleware(router, cfg, false)
	mountMetrics()
	router.HandleFunc("/*", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return router
}

// TestBuild_AssemblesWithMetricsEnabled guards the assembly order itself. Chi
// panics if Use is called once any route exists on the mux, so mounting
// /metrics in the middle of the middleware stack took the server down at boot
// for every deployment that had not set METRICS_ENABLED=false. No database is
// touched before the router is built, so a pool that never connects is enough
// to exercise it.
func TestBuild_AssemblesWithMetricsEnabled(t *testing.T) {
	t.Setenv("METRICS_ENABLED", "true")

	pool, err := pgxpool.New(context.Background(), "postgres://u:p@127.0.0.1:1/nowhere?sslmode=disable")
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	srv, err := NewBuilder(pool, &platform.Config{StoragePath: t.TempDir(), ListenAddr: ":0"}).Build()
	require.NoError(t, err)
	require.NotNil(t, srv.Handler)

	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	assert.Equal(t, http.StatusOK, rec.Code)
}

// countAllowed fires n requests through h and reports how many were not
// rate limited.
func countAllowed(t *testing.T, h http.Handler, n int, build func(i int) *http.Request) int {
	t.Helper()
	allowed := 0
	for i := 0; i < n; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, build(i))
		if rec.Code != http.StatusTooManyRequests {
			allowed++
		}
	}
	return allowed
}

func login(remoteAddr string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	r.RemoteAddr = remoteAddr
	return r
}

// TestEdgeChain_ForgedHeadersCannotEscapeAuthLimit is the regression test for
// header-spoofed rate limit evasion. Chi's RealIP middleware used to overwrite
// RemoteAddr from X-Real-IP / X-Forwarded-For with no notion of a trusted
// proxy, so a single attacker could mint a fresh brute-force budget on every
// login attempt just by changing a header — and, because the per-IP ceiling is
// what bounds the limiter's subject maps, grow those maps without limit too.
//
// With TRUSTED_PROXIES unset, no forwarding header may be believed: all three
// cases below must be held to the same budget as an unadorned request.
func TestEdgeChain_ForgedHeadersCannotEscapeAuthLimit(t *testing.T) {
	const attempts = 1000
	const limit = 5

	cases := []struct {
		name   string
		header string
	}{
		{"no forged headers", ""},
		{"rotating X-Real-IP", "X-Real-IP"},
		{"rotating X-Forwarded-For", "X-Forwarded-For"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := edgeChain(t, &platform.Config{RateLimitAuth: limit})

			allowed := countAllowed(t, h, attempts, func(i int) *http.Request {
				r := login("203.0.113.9:5000")
				if tc.header != "" {
					r.Header.Set(tc.header, fmt.Sprintf("198.51.100.%d", i%256))
				}
				return r
			})

			t.Logf("%s: %d/%d allowed", tc.name, allowed, attempts)
			assert.LessOrEqualf(t, allowed, limit,
				"a client-supplied forwarding header must not escape the auth budget (got %d/%d allowed)",
				allowed, attempts)
		})
	}
}

// TestEdgeChain_TrustedProxyIsBelieved covers the other half: once the
// operator declares the proxy trusted, the forwarded address must be used, and
// used per client — otherwise every request through the proxy shares one
// budget and the whole internet is throttled as a single client.
func TestEdgeChain_TrustedProxyIsBelieved(t *testing.T) {
	const limit = 5

	for _, header := range []string{"X-Real-IP", "X-Forwarded-For"} {
		t.Run(header, func(t *testing.T) {
			h := edgeChain(t, &platform.Config{
				RateLimitAuth:  limit,
				TrustedProxies: "10.0.0.0/8",
			})

			build := func(client string) func(int) *http.Request {
				return func(int) *http.Request {
					r := login("10.4.4.4:5000")
					r.Header.Set(header, client)
					return r
				}
			}

			first := countAllowed(t, h, 20, build("198.51.100.7"))
			require.Equal(t, limit, first, "a forwarded client gets exactly its own budget")

			second := countAllowed(t, h, 20, build("198.51.100.8"))
			assert.Equal(t, limit, second,
				"a second client behind the same trusted proxy must not inherit the first client's exhausted budget")
		})
	}
}

// TestEdgeChain_TrustedProxyPreservesLimiterProperties re-proves, through the
// full chain and behind a trusted proxy, the two properties the limiter was
// built for: distinct API keys from one source address keep independent
// budgets, and the shared per-IP ceiling still bounds an attacker who rotates
// a fresh key on every request.
func TestEdgeChain_TrustedProxyPreservesLimiterProperties(t *testing.T) {
	// Read: 3 per subject, so the IP ceiling is 3 × 10 = 30.
	h := edgeChain(t, &platform.Config{
		RateLimitAuth:   20,
		RateLimitEvents: 20,
		RateLimitRead:   3,
		RateLimitWrite:  20,
		TrustedProxies:  "10.0.0.0/8",
	})

	// Both developers sit behind one office NAT, reaching skael through the
	// trusted proxy: same forwarded client address, different API keys.
	build := func(key string) func(int) *http.Request {
		return func(int) *http.Request {
			r := httptest.NewRequest(http.MethodGet, "/api/skills", nil)
			r.RemoteAddr = "10.4.4.4:5000"
			r.Header.Set("X-Forwarded-For", "198.51.100.20")
			r.Header.Set("X-API-Key", key)
			return r
		}
	}

	allowedA := countAllowed(t, h, 5, build("sk-fixed-aaaaaaaaaaaa"))
	require.Equal(t, 3, allowedA, "first developer gets a full independent budget")

	allowedB := countAllowed(t, h, 5, build("sk-fixed-bbbbbbbbbbbb"))
	require.Equal(t, 3, allowedB, "second developer behind the same address keeps their own budget")

	// 10 of the 30 ceiling tokens are spent (every attempt is charged to the
	// IP bucket, allowed or not), leaving 20. A key-rotating attacker never
	// trips its own per-key bucket, so the ceiling is all that bounds it.
	allowedRotating := countAllowed(t, h, 40, func(i int) *http.Request {
		return build(fmt.Sprintf("sk-rotate-%d", i))(i)
	})
	assert.Equal(t, 20, allowedRotating,
		"the shared per-IP ceiling must still bound a key-rotating attacker behind a trusted proxy")
}
