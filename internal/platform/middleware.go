package platform

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/time/rate"
)

type requestIDKeyType struct{}

var requestIDKey = requestIDKeyType{}

// RequestIDFromContext returns the request ID stored in ctx, or "".
func RequestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// RequestID is a Chi-compatible middleware that reads X-Request-ID from the
// incoming request, or generates a UUID if absent. It sets the ID on the
// response header and in the request context.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			id = uuid.New().String()
		}
		w.Header().Set("X-Request-ID", id)
		ctx := context.WithValue(r.Context(), requestIDKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// SecurityHeaders returns middleware that sets common security response headers.
// When cookieSecure is true, Strict-Transport-Security is also set.
func SecurityHeaders(cookieSecure bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("X-Frame-Options", "DENY")
			w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
			w.Header().Set("Content-Security-Policy",
				"default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; font-src 'self' data:; img-src 'self' data:; connect-src 'self'")
			if cookieSecure {
				w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
			}
			next.ServeHTTP(w, r)
		})
	}
}

type limiterEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// RateLimiter returns middleware that enforces per-IP rate limiting.
// requestsPerMinute controls the sustained rate; window is unused but reserved
// for future burst tuning. Clients exceeding the limit receive 429 with a
// Retry-After header. Stale entries are cleaned up on access.
func RateLimiter(requestsPerMinute int, _ time.Duration) func(http.Handler) http.Handler {
	var mu sync.Mutex
	visitors := make(map[string]*limiterEntry)

	cleanup := func() {
		cutoff := time.Now().Add(-10 * time.Minute)
		for ip, entry := range visitors {
			if entry.lastSeen.Before(cutoff) {
				delete(visitors, ip)
			}
		}
	}

	getLimiter := func(ip string) *rate.Limiter {
		mu.Lock()
		defer mu.Unlock()

		entry, exists := visitors[ip]
		if exists {
			entry.lastSeen = time.Now()
			return entry.limiter
		}

		cleanup()

		r := rate.Every(time.Minute / time.Duration(requestsPerMinute))
		limiter := rate.NewLimiter(r, requestsPerMinute)
		visitors[ip] = &limiterEntry{limiter: limiter, lastSeen: time.Now()}
		return limiter
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				ip = r.RemoteAddr
			}

			limiter := getLimiter(ip)
			if !limiter.Allow() {
				w.Header().Set("Retry-After", "60")
				http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// envInt reads an integer from an environment variable, returning fallback on
// parse error or missing value.
func envInt(key string, fallback int) int {
	v := envDefault(key, "")
	if v == "" {
		return fallback
	}
	var n int
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil {
		return fallback
	}
	return n
}
