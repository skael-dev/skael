package server

import (
	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/skael-dev/skael/internal/platform"
)

// RouteRegistrar is a function that registers additional routes onto the Huma
// API and Chi router. Enterprise plugins use this to add their own endpoints.
type RouteRegistrar func(api huma.API, router chi.Router, pool *pgxpool.Pool)

// Builder assembles a Server from its constituent parts. The zero value is not
// useful — use NewBuilder. Callers chain With* methods to opt into enterprise
// extension points, then call Build to get a runnable Server.
type Builder struct {
	pool        *pgxpool.Pool
	config      *platform.Config
	authorizer  Authorizer
	audit       AuditLogger
	idp         IdentityProvider
	policy      PolicyEnforcer
	scanRules   ScanRuleProvider
	extraRoutes []RouteRegistrar
	caps        *Capabilities
}

// NewBuilder creates a Builder with OSS defaults: no extension points, no
// enterprise features, edition "oss".
func NewBuilder(pool *pgxpool.Pool, config *platform.Config) *Builder {
	return &Builder{
		pool:   pool,
		config: config,
		caps:   NewCapabilities(),
	}
}

// WithAuthorizer attaches an Authorizer (RBAC) and enables the rbac and teams
// feature flags.
func (b *Builder) WithAuthorizer(a Authorizer) *Builder {
	b.authorizer = a
	b.caps.EnableFeature("rbac")
	b.caps.EnableFeature("teams")
	return b
}

// WithAuditLog attaches an AuditLogger and enables the audit feature flag.
func (b *Builder) WithAuditLog(l AuditLogger) *Builder {
	b.audit = l
	b.caps.EnableFeature("audit")
	return b
}

// WithIdentityProvider attaches an IdentityProvider (SSO) and enables the sso
// feature flag.
func (b *Builder) WithIdentityProvider(idp IdentityProvider) *Builder {
	b.idp = idp
	b.caps.EnableFeature("sso")
	return b
}

// WithPolicyEnforcer attaches a PolicyEnforcer and enables the governance
// feature flag.
func (b *Builder) WithPolicyEnforcer(p PolicyEnforcer) *Builder {
	b.policy = p
	b.caps.EnableFeature("governance")
	return b
}

// WithScanRules attaches a ScanRuleProvider and enables the custom_scan
// feature flag.
func (b *Builder) WithScanRules(s ScanRuleProvider) *Builder {
	b.scanRules = s
	b.caps.EnableFeature("custom_scan")
	return b
}

// WithRoutes registers additional route registrars that are called during
// Build after all core routes are wired. Enterprise plugins use this to inject
// their own endpoints.
func (b *Builder) WithRoutes(registrars ...RouteRegistrar) *Builder {
	b.extraRoutes = append(b.extraRoutes, registrars...)
	return b
}

// WithEdition overrides the edition string (e.g. "enterprise") in capability
// responses.
func (b *Builder) WithEdition(edition string) *Builder {
	b.caps.SetEdition(edition)
	return b
}

// WithLicense sets the license metadata returned by the capabilities endpoint.
func (b *Builder) WithLicense(l *LicenseInfo) *Builder {
	b.caps.SetLicense(l)
	return b
}

// Capabilities returns the Capabilities value accumulated so far. This is
// primarily useful for testing — in production you call Build.
func (b *Builder) Capabilities() *Capabilities {
	return b.caps
}
