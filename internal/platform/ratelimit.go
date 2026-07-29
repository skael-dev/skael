package platform

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// RateLimitConfig holds the per-minute request budget for each route class.
// The classes exist because one number cannot serve all of them: hook events
// arrive once per skill activation across a whole team, archive downloads
// arrive in bursts during a sync, and login attempts are a brute-force surface
// that should stay tight.
type RateLimitConfig struct {
	Auth   int // /api/auth/* — login, signup, password reset
	Events int // POST /api/events — activation tracking
	Read   int // GET/HEAD — list, search, manifest, downloads
	Write  int // everything else — publish, import, delete
}

type rateLimitClass string

const (
	classAuth   rateLimitClass = "auth"
	classEvents rateLimitClass = "events"
	classRead   rateLimitClass = "read"
	classWrite  rateLimitClass = "write"
)

// Defaults chosen so a ten-person team syncing a hundred-skill registry never
// sees a 429 during normal work, while login stays brute-force resistant.
const (
	defaultRateLimitAuth   = 20
	defaultRateLimitEvents = 600
	defaultRateLimitRead   = 300
	defaultRateLimitWrite  = 60
)

func classifyRequest(r *http.Request) rateLimitClass {
	switch {
	case strings.HasPrefix(r.URL.Path, "/api/auth/"):
		return classAuth
	case r.URL.Path == "/api/events":
		return classEvents
	case r.Method == http.MethodGet || r.Method == http.MethodHead:
		return classRead
	default:
		return classWrite
	}
}

// rateLimitSubject identifies who a request is charged to. An API key is a far
// better identity than an IP: a whole team behind one NAT shares an address,
// and throttling them as a single client is the bug this replaces. The key is
// hashed so raw credentials never sit in the limiter map.
func rateLimitSubject(r *http.Request) string {
	if key := r.Header.Get("X-API-Key"); key != "" {
		sum := sha256.Sum256([]byte(key))
		return "key:" + hex.EncodeToString(sum[:8])
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	return "ip:" + ip
}

type limiterEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// limiterSet holds one token bucket per subject for a single route class.
type limiterSet struct {
	mu       sync.Mutex
	subjects map[string]*limiterEntry
	perMin   int
}

func newLimiterSet(perMin, fallback int) *limiterSet {
	if perMin <= 0 {
		perMin = fallback
	}
	return &limiterSet{subjects: make(map[string]*limiterEntry), perMin: perMin}
}

func (s *limiterSet) allow(subject string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.subjects[subject]
	if !ok {
		// Drop subjects that have been quiet for ten minutes so the map does
		// not grow without bound.
		cutoff := time.Now().Add(-10 * time.Minute)
		for k, v := range s.subjects {
			if v.lastSeen.Before(cutoff) {
				delete(s.subjects, k)
			}
		}
		entry = &limiterEntry{
			limiter: rate.NewLimiter(rate.Every(time.Minute/time.Duration(s.perMin)), s.perMin),
		}
		s.subjects[subject] = entry
	}
	entry.lastSeen = time.Now()

	return entry.limiter.Allow()
}

// ClassifiedRateLimiter returns middleware that applies a separate per-minute
// budget to each route class, keyed by API key where present and by IP
// otherwise. Clients over the limit receive 429 with a Retry-After header,
// which the CLI honours.
func ClassifiedRateLimiter(cfg RateLimitConfig) func(http.Handler) http.Handler {
	sets := map[rateLimitClass]*limiterSet{
		classAuth:   newLimiterSet(cfg.Auth, defaultRateLimitAuth),
		classEvents: newLimiterSet(cfg.Events, defaultRateLimitEvents),
		classRead:   newLimiterSet(cfg.Read, defaultRateLimitRead),
		classWrite:  newLimiterSet(cfg.Write, defaultRateLimitWrite),
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !sets[classifyRequest(r)].allow(rateLimitSubject(r)) {
				w.Header().Set("Retry-After", "60")
				http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
