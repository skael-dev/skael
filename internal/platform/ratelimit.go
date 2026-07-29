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

// ipCeilingFactor sizes the shared, IP-keyed ceiling bucket applied to every
// non-auth class: the ceiling's burst is ipCeilingFactor times the class's
// per-subject limit. It exists because a per-subject budget keyed by API key
// can be defeated by rotating the key — each unverified key mints a fresh
// bucket. Charging every request to the requesting IP first, before the
// per-key bucket is even consulted, bounds what a single source can do
// overall regardless of how many keys it presents. The factor is sized
// generously enough that a real team of developers sharing one NAT — the
// case this limiter exists to support — never trips it in normal use.
const ipCeilingFactor = 10

// limiterGenerationInterval controls how often a limiterSet retires its
// oldest generation of per-subject buckets. See limiterSet for the eviction
// scheme.
const limiterGenerationInterval = 10 * time.Minute

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

// rateLimitSubject identifies who a non-auth request is charged to at the
// per-subject level. An API key is a far better identity than an IP: a whole
// team behind one NAT shares an address, and throttling them as a single
// client is the bug this replaces. The key is hashed so raw credentials
// never sit in the limiter map.
//
// This must NEVER be used for the auth class. Login/signup requests are
// unauthenticated by definition, so X-API-Key on them is unverified — an
// attacker can put anything there, and keying the brute-force budget on an
// unverified header lets them mint a fresh budget on every attempt. The auth
// class is keyed on IP alone via ipSubject instead; ClassifiedRateLimiter
// never calls rateLimitSubject for classAuth.
func rateLimitSubject(r *http.Request) string {
	if key := r.Header.Get("X-API-Key"); key != "" {
		sum := sha256.Sum256([]byte(key))
		return "key:" + hex.EncodeToString(sum[:8])
	}
	return ipSubject(r)
}

// ipSubject identifies a request by source IP only, ignoring any API key. It
// is the sole subject for the auth class, and the ceiling-bucket subject for
// every other class.
//
// The address comes from ClientIPFromRequest, which yields a forwarded address
// only when the peer is a configured trusted proxy and the socket address
// otherwise. That distinction is what makes this bucket meaningful: a value
// taken straight from X-Forwarded-For would let one attacker mint a new
// identity per request, escaping every budget below and growing these maps
// without bound.
func ipSubject(r *http.Request) string {
	return "ip:" + ClientIPFromRequest(r)
}

// limiterSet holds one token bucket per subject for a single budget (either
// a whole class, or one half of a class's two-bucket pair).
//
// Buckets live in two generations, current and previous, instead of a single
// map swept for staleness on every miss. A lookup checks current, then
// previous — promoting on hit so an active subject keeps its bucket — and
// falls back to creating a fresh entry in current. Roughly every
// limiterGenerationInterval, the whole set rotates: previous is dropped (by
// replacement, not iteration) and current becomes the new previous. This is
// O(1) per request with no scan of the map, and bounds memory to the
// subjects seen in the last two intervals.
type limiterSet struct {
	mu              sync.Mutex
	current         map[string]*rate.Limiter
	previous        map[string]*rate.Limiter
	generationStart time.Time
	perMin          int
}

// newLimiterSet creates a limiterSet whose buckets allow perMin requests per
// minute, bursting up to perMin. perMin must already reflect any configured
// default — this constructor performs no fallback substitution.
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

// classLimiter is the pair of buckets a non-auth class checks against: ip is
// a shared ceiling per source address, subject is the per-API-key (or,
// anonymously, per-IP) budget. A request is allowed only if both allow.
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

// ClassifiedRateLimiter returns middleware that applies a separate per-minute
// budget to each route class.
//
// The auth class (login, signup, password reset) is keyed on IP alone: these
// requests are unauthenticated, so any X-API-Key header on them is unverified
// and must never influence the brute-force budget.
//
// Every other class is checked against two buckets, both of which must
// allow: a shared IP ceiling (ipCeilingFactor × the class limit), checked
// first, and a per-subject budget keyed by API key where present and by IP
// otherwise. Checking the IP bucket first means a denial there rejects the
// request immediately without ever touching — or creating — a per-subject
// bucket, so a burst of forged keys is stopped by the shared ceiling before
// it can grow the per-subject map.
//
// Clients over either limit receive 429 with a Retry-After header, which the
// CLI honours.
func ClassifiedRateLimiter(cfg RateLimitConfig) func(http.Handler) http.Handler {
	authIP := newLimiterSet(effectiveLimit(cfg.Auth, defaultRateLimitAuth))

	nonAuth := map[rateLimitClass]classLimiter{
		classEvents: newClassLimiter(cfg.Events, defaultRateLimitEvents),
		classRead:   newClassLimiter(cfg.Read, defaultRateLimitRead),
		classWrite:  newClassLimiter(cfg.Write, defaultRateLimitWrite),
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
