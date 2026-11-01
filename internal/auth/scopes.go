package auth

import (
	"context"
	"net/http"
	"slices"
	"strings"
)

// The scopes a key may hold.
const (
	ScopeSkillsRead = "skills:read"
	ScopeSetsRead   = "sets:read"
	ScopeEvalWork   = "eval:work"

	ScopeEventsWrite      = "events:write"
	ScopeOutcomesWrite    = "outcomes:write"
	ScopeSubmissionsWrite = "submissions:write"
)

// ValidScope reports whether s is a scope the server knows.
func ValidScope(s string) bool {
	return slices.Contains([]string{
		ScopeSkillsRead, ScopeSetsRead, ScopeEvalWork,
		ScopeEventsWrite, ScopeOutcomesWrite, ScopeSubmissionsWrite,
	}, s)
}

// RouteRule says which scopes reach one route. A rule with no scopes is listed
// so that the route is a decision, and a restricted key still never reaches it.
type RouteRule struct {
	Method  string
	Pattern string
	Scopes  []string
	// SetLimited marks a route a set-limited key may reach. The handler then
	// checks that the set is the key's own.
	SetLimited bool
}

func read(pattern string, scopes ...string) RouteRule {
	return RouteRule{Method: http.MethodGet, Pattern: pattern, Scopes: scopes}
}

func setRead(pattern string) RouteRule {
	return RouteRule{Method: http.MethodGet, Pattern: pattern, Scopes: []string{ScopeSetsRead}, SetLimited: true}
}

func post(pattern string, scopes ...string) RouteRule {
	return RouteRule{Method: http.MethodPost, Pattern: pattern, Scopes: scopes}
}

func put(pattern string) RouteRule { return RouteRule{Method: http.MethodPut, Pattern: pattern} }

func del(pattern string) RouteRule { return RouteRule{Method: http.MethodDelete, Pattern: pattern} }

// routeRules is the deny-by-default table for restricted keys. A test in
// internal/server walks every registered route and fails on one missing here.
var routeRules = []RouteRule{
	// Machine reads of the registry.
	read("/api/skills", ScopeSkillsRead),
	read("/api/skills/{name}", ScopeSkillsRead),
	read("/api/skills/{name}/versions", ScopeSkillsRead),
	read("/api/skills/{name}/versions/{version}/download", ScopeSkillsRead, ScopeEvalWork),
	read("/api/skills/{name}/versions/{version}/files/*", ScopeSkillsRead),
	read("/api/skills/{name}/versions/{version}/diff", ScopeSkillsRead),
	read("/api/skills/{name}/scan", ScopeSkillsRead),
	read("/api/skills/{name}/aliases", ScopeSkillsRead),
	read("/api/skills/{name}/owners", ScopeSkillsRead),
	read("/api/skills/{name}/source", ScopeSkillsRead),
	read("/api/skills/{name}/quality", ScopeSkillsRead),
	read("/api/skills/{name}/quality/history", ScopeSkillsRead),
	read("/api/skills/{name}/quality/series", ScopeSkillsRead),
	read("/api/skills/{name}/quality/{version}", ScopeSkillsRead),
	read("/api/skills/tags", ScopeSkillsRead),
	read("/api/search", ScopeSkillsRead),
	read("/api/sync/manifest", ScopeSkillsRead),

	// Sets.
	setRead("/api/sets"),
	setRead("/api/sets/{name}"),
	setRead("/api/sets/{name}/resolve"),
	setRead("/api/sets/{name}/resolutions/{id}"),
	setRead("/api/sets/{name}/bundle"),
	post("/api/sets"),
	put("/api/sets/{name}"),
	del("/api/sets/{name}"),

	// The worker's loop.
	post("/api/eval/jobs/claim", ScopeEvalWork),
	post("/api/eval/jobs/{id}/heartbeat", ScopeEvalWork),
	post("/api/eval/jobs/{id}/fail", ScopeEvalWork),
	post("/api/eval/jobs/{id}/report", ScopeEvalWork),
	post("/api/eval/contests/{id}/report", ScopeEvalWork),
	post("/api/eval/suites", ScopeEvalWork),
	read("/api/eval/suites/{ref}", ScopeEvalWork),
	read("/api/eval/suites/{ref}/meta", ScopeEvalWork),

	// Run signals. A person decides a submission, so decide and list have no scope.
	post("/api/events/batch", ScopeEventsWrite),
	post("/api/outcomes", ScopeOutcomesWrite),
	read("/api/skills/{name}/outcomes", ScopeSkillsRead),
	post("/api/submissions", ScopeSubmissionsWrite),
	read("/api/submissions/{id}", ScopeSubmissionsWrite),
	read("/api/submissions"),
	read("/api/submissions/{id}/diff"),
	post("/api/submissions/{id}/decision"),

	// MCP. Each tool checks its own scope.
	{Method: http.MethodPost, Pattern: "/mcp", Scopes: []string{ScopeSkillsRead, ScopeSetsRead, ScopeOutcomesWrite, ScopeSubmissionsWrite}, SetLimited: true},
	// GET only answers 405. It has no scope, so a restricted key gets 403 first.
	read("/mcp"),

	// No scope: people only. Public routes are listed too, because the auth
	// middleware passes them before this table applies.
	read("/api/health"), read("/api/health/ready"), read("/api/capabilities"),
	post("/api/auth/signup"), post("/api/auth/login"), post("/api/auth/logout"),
	read("/api/auth/me"), read("/api/auth/keys"), post("/api/auth/keys"), del("/api/auth/keys/{id}"),
	post("/api/auth/change-password"),
	post("/api/admin/reset-password"), read("/api/admin/users"), put("/api/admin/users/{id}/role"),
	read("/api/admin/audit"), read("/api/users/search"),
	read("/api/service-accounts"), post("/api/service-accounts"), del("/api/service-accounts/{id}"),
	read("/api/service-accounts/{id}/keys"), post("/api/service-accounts/{id}/keys"),
	del("/api/service-accounts/{id}/keys/{keyId}"),
	post("/api/skills"), post("/api/skills/register"), del("/api/skills/{name}"),
	post("/api/skills/{name}/versions"), post("/api/skills/{name}/versions/{version}/review"),
	put("/api/skills/review"), put("/api/skills/{name}/review"),
	del("/api/skills/{name}/review"),
	post("/api/skills/{name}/aliases"), del("/api/skills/{name}/aliases/{alias}"), post("/api/skills/merge"),
	read("/api/review/queue"),
	read("/api/ownership/rules"), post("/api/ownership/rules"),
	put("/api/ownership/rules/{id}"), del("/api/ownership/rules/{id}"),
	read("/api/eval/jobs/{id}"), post("/api/eval/jobs/{id}/cancel"),
	read("/api/skills/{name}/evals"), post("/api/skills/{name}/evals"),
	read("/api/eval/suites/{ref}/triggers"), post("/api/eval/suites/{ref}/review"),
	post("/api/eval/contests"), read("/api/eval/contests/{id}"), read("/api/skills/{name}/contests"),
	post("/api/import/resolve"), post("/api/import"), read("/api/import/sources"), post("/api/import/upload"),
	post("/api/events"), read("/api/analytics/overview"), read("/api/analytics/skills"),
	read("/api/skills/{name}/activations"), read("/api/skills/{name}/timeseries"),
	read("/api/analytics/timeseries"), read("/api/analytics/unregistered"), post("/api/analytics/dismiss"),
}

// RouteRules returns the table for the coverage test.
func RouteRules() []RouteRule { return routeRules }

// matchRule finds the rule for a request path, written as chi sees it. When
// several patterns match, the most literal wins, as it does in chi.
func matchRule(method, path string) *RouteRule {
	segs := strings.Split(path, "/")
	var best *RouteRule
	bestLiterals := -1
	for i := range routeRules {
		rule := &routeRules[i]
		if rule.Method != method {
			continue
		}
		if literals, ok := matchPattern(rule.Pattern, segs); ok && literals > bestLiterals {
			best, bestLiterals = rule, literals
		}
	}
	return best
}

func matchPattern(pattern string, segs []string) (literals int, ok bool) {
	parts := strings.Split(pattern, "/")
	for i, p := range parts {
		if p == "*" {
			return literals, i < len(segs)
		}
		if i >= len(segs) {
			return 0, false
		}
		switch {
		case strings.HasPrefix(p, "{"):
			if segs[i] == "" {
				return 0, false
			}
		case p == segs[i]:
			literals++
		default:
			return 0, false
		}
	}
	return literals, len(parts) == len(segs)
}

// KeyInfo describes the API key that authenticated a request. A session or an
// unrestricted personal key has none of these limits.
type KeyInfo struct {
	ID     string
	Scopes []string
	SetID  string
}

// Restricted reports whether the deny-by-default table applies to the key.
func (k *KeyInfo) Restricted() bool {
	return k != nil && (len(k.Scopes) > 0 || k.SetID != "")
}

type keyContextKey struct{}

// ContextWithKey attaches the authenticating key to ctx.
func ContextWithKey(ctx context.Context, k *KeyInfo) context.Context {
	return context.WithValue(ctx, keyContextKey{}, k)
}

// KeyFromContext returns the authenticating key, or nil for a session.
func KeyFromContext(ctx context.Context) *KeyInfo {
	k, _ := ctx.Value(keyContextKey{}).(*KeyInfo)
	return k
}

// HasScope reports whether the request's key holds scope explicitly. An
// unrestricted key holds no scopes: its rights come from its owner's role.
func HasScope(ctx context.Context, scope string) bool {
	k := KeyFromContext(ctx)
	return k != nil && slices.Contains(k.Scopes, scope)
}

// Allowed reports whether a tool or handler that needs scope may run for the
// request: an unrestricted caller may, and a restricted key needs the scope.
func Allowed(ctx context.Context, scope string) bool {
	k := KeyFromContext(ctx)
	return !k.Restricted() || slices.Contains(k.Scopes, scope)
}

// authorizeKey applies the table to a restricted key.
func authorizeKey(k *KeyInfo, r *http.Request) bool {
	path := r.URL.RawPath
	if path == "" {
		path = r.URL.Path
	}
	rule := matchRule(r.Method, path)
	if rule == nil || !slices.ContainsFunc(rule.Scopes, func(s string) bool { return slices.Contains(k.Scopes, s) }) {
		return false
	}
	return k.SetID == "" || rule.SetLimited
}
