package platform

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// RateLimitConfig holds the per-minute request budget for each route class.
type RateLimitConfig struct {
	Auth   int
	Events int
	Read   int
	Write  int
	Suites int
}

type rateLimitClass string

const (
	classAuth   rateLimitClass = "auth"
	classEvents rateLimitClass = "events"
	classRead   rateLimitClass = "read"
	classWrite  rateLimitClass = "write"
	classSuites rateLimitClass = "suites"
)

const (
	defaultRateLimitAuth   = 20
	defaultRateLimitEvents = 600
	defaultRateLimitRead   = 300
	defaultRateLimitWrite  = 60
	defaultRateLimitSuites = 20
)

// ipCeilingFactor bounds what one source IP can do regardless of how many
// API keys it presents — rotating keys otherwise mints a fresh per-key bucket
// on every request.
const ipCeilingFactor = 10

const limiterGenerationInterval = 10 * time.Minute

func classifyRequest(r *http.Request) rateLimitClass {
	switch {
	case strings.HasPrefix(r.URL.Path, "/api/auth/"):
		return classAuth
	case r.URL.Path == "/api/events":
		return classEvents
	case r.Method == http.MethodGet || r.Method == http.MethodHead:
		return classRead
	case r.URL.Path == "/api/eval/suites" && r.Method == http.MethodPost:
		return classSuites
	default:
		return classWrite
	}
}

// rateLimitSubject keys on a hash of the API key when present, IP otherwise.
// Never used for the auth class — login requests are unauthenticated, so
// X-API-Key there is unverified and would let an attacker mint a fresh budget
// on every attempt.
func rateLimitSubject(r *http.Request) string {
	if key := r.Header.Get("X-API-Key"); key != "" {
		sum := sha256.Sum256([]byte(key))
		return "key:" + hex.EncodeToString(sum[:8])
	}
	return ipSubject(r)
}

// ipSubject keys on the resolved client IP. The address comes from
// ClientIPFromRequest, which trusts forwarded headers only from configured
// proxies — trusting them unconditionally lets an attacker mint a new
// identity per request.
func ipSubject(r *http.Request) string {
	return "ip:" + ClientIPFromRequest(r)
}

// limiterSet holds one token bucket per subject. Two-generation eviction:
// current and previous rotate every limiterGenerationInterval, bounding
// memory to subjects seen in the last two intervals with no per-request scan.
type limiterSet struct {
	mu              sync.Mutex
	current         map[string]*rate.Limiter
	previous        map[string]*rate.Limiter
	generationStart time.Time
	perMin          int
}

// newLimiterSet creates a limiterSet with the given per-minute budget.
func newLimiterSet(perMin int) *limiterSet {
	return &limiterSet{
		current:         make(map[string]*rate.Limiter),
		previous:        make(map[string]*rate.Limiter),
		generationStart: time.Now(),
		perMin:          perMin,
	}
}

func (s *limiterSet) allow(subject string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if time.Since(s.generationStart) >= limiterGenerationInterval {
		s.previous = s.current
		s.current = make(map[string]*rate.Limiter)
		s.generationStart = time.Now()
	}

	if lim, ok := s.current[subject]; ok {
		return lim.Allow()
	}
	if lim, ok := s.previous[subject]; ok {
		delete(s.previous, subject)
		s.current[subject] = lim
		return lim.Allow()
	}

	lim := rate.NewLimiter(rate.Every(time.Minute/time.Duration(s.perMin)), s.perMin)
	s.current[subject] = lim
	return lim.Allow()
}

// classLimiter pairs a shared IP ceiling with a per-subject budget.
type classLimiter struct {
	ip      *limiterSet
	subject *limiterSet
}

func effectiveLimit(configured, fallback int) int {
	if configured <= 0 {
		return fallback
	}
	return configured
}

func newClassLimiter(configured, fallback int) classLimiter {
	limit := effectiveLimit(configured, fallback)
	return classLimiter{
		ip:      newLimiterSet(limit * ipCeilingFactor),
		subject: newLimiterSet(limit),
	}
}

// ClassifiedRateLimiter returns middleware with a separate per-minute budget
// per route class. Auth is IP-only; other classes check an IP ceiling first
// (stops forged-key bursts before they grow the per-subject map), then a
// per-subject budget keyed by API key or IP.
func ClassifiedRateLimiter(cfg RateLimitConfig) func(http.Handler) http.Handler {
	authIP := newLimiterSet(effectiveLimit(cfg.Auth, defaultRateLimitAuth))

	nonAuth := map[rateLimitClass]classLimiter{
		classEvents: newClassLimiter(cfg.Events, defaultRateLimitEvents),
		classRead:   newClassLimiter(cfg.Read, defaultRateLimitRead),
		classWrite:  newClassLimiter(cfg.Write, defaultRateLimitWrite),
		classSuites: newClassLimiter(cfg.Suites, defaultRateLimitSuites),
	}

	reject := func(w http.ResponseWriter) {
		w.Header().Set("Retry-After", "60")
		http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			class := classifyRequest(r)

			if class == classAuth {
				if !authIP.allow(ipSubject(r)) {
					reject(w)
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			cl := nonAuth[class]
			if !cl.ip.allow(ipSubject(r)) {
				reject(w)
				return
			}
			if !cl.subject.allow(rateLimitSubject(r)) {
				reject(w)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
