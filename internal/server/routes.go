package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"

	"github.com/skael-dev/skael/internal/analytics"
	"github.com/skael-dev/skael/internal/auth"
	"github.com/skael-dev/skael/internal/evalqueue"
	"github.com/skael-dev/skael/internal/evalsuite"
	skillimport "github.com/skael-dev/skael/internal/import"
	"github.com/skael-dev/skael/internal/platform"
	"github.com/skael-dev/skael/internal/quality"
	"github.com/skael-dev/skael/internal/scan"
	"github.com/skael-dev/skael/internal/skill"
	gosync "github.com/skael-dev/skael/internal/sync"
)

// RegisterAPIDeps bundles everything RegisterAPIRoutes needs to wire the API.
// Every field except Pool, Router, and Caps may be nil (or the pool may be
// nil): RegisterAPIRoutes is used both by the real server, where every field
// is a live dependency, and by `skael-server --openapi`, which only needs the
// route shapes for spec generation and never executes a handler. The stores
// constructed here (skill.NewStore, evalqueue.NewPool, etc.) hold their pool
// reference lazily and don't touch it until a handler runs, so a nil pool is
// safe for registration-only use.
//
// This is the single place both paths register routes from, specifically so
// they should not drift apart — see cmd/server/main.go's printOpenAPISpec.
// The guard test (routes_test.go) only checks routes registered inside this
// function; a new route group must be added here, not via a direct
// huma.Register call in Build() after RegisterAPIRoutes returns, or the
// guard cannot see it and the spec can silently drift again.
type RegisterAPIDeps struct {
	Pool           *pgxpool.Pool
	Config         *platform.Config
	SessionManager *scs.SessionManager
	UserStore      *auth.UserStore
	KeyStore       *auth.KeyStore
	Storage        platform.Storage
	Caps           *Capabilities
	// AnalyticsStore is optional; if nil, RegisterAPIRoutes constructs one
	// from Pool. Build() passes its own instance so the same store can be
	// reused for startup event-retention cleanup instead of constructing a
	// second one.
	AnalyticsStore *analytics.Store
}

// RegisterAPIRoutes registers every /api/* route: health, readiness,
// capabilities, auth, skills, sync manifest, analytics, import, eval suites,
// eval queue, and quality. It does not register the embedded SPA catch-all —
// callers that serve the SPA (only Build) mount that separately, after this
// returns, per Huma/chi ordering requirements.
//
// It returns the eval queue's PoolExecutor so Build can start the lease
// reaper against the same queue this registered — constructing a second
// PoolExecutor there would work against the same table but would be a
// needless second handle, and would drift from this function if the two
// ever needed to differ.
func RegisterAPIRoutes(api huma.API, router chi.Router, d RegisterAPIDeps) *evalqueue.PoolExecutor {
	cfg := d.Config
	if cfg == nil {
		cfg = &platform.Config{}
	}

	// Health (auth middleware skips /api/health).
	huma.Register(api, huma.Operation{
		OperationID: "health",
		Method:      http.MethodGet,
		Path:        "/api/health",
	}, func(ctx context.Context, input *struct{}) (*struct {
		Body struct {
			Status string `json:"status"`
		}
	}, error) {
		out := &struct {
			Body struct {
				Status string `json:"status"`
			}
		}{}
		out.Body.Status = "ok"
		return out, nil
	})

	// Capabilities.
	caps := d.Caps
	if caps == nil {
		caps = NewCapabilities()
	}
	caps.Register(api)

	// Readiness: verifies DB and storage connectivity.
	type readyBody struct {
		Status string      `json:"status"`
		Checks ReadyChecks `json:"checks"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "health-ready",
		Method:      http.MethodGet,
		Path:        "/api/health/ready",
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body readyBody }, error) {
		checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()

		checks, ready := readinessChecks(checkCtx, d.Pool, d.Storage)
		if !ready {
			detail, _ := json.Marshal(checks)
			return nil, huma.NewError(http.StatusServiceUnavailable, "not ready", fmt.Errorf("%s", detail))
		}
		out := &struct{ Body readyBody }{}
		out.Body.Status = "ready"
		out.Body.Checks = checks
		return out, nil
	})

	// Auth.
	auth.RegisterRoutes(api, d.SessionManager, d.UserStore, d.KeyStore, cfg.DisableSignup)

	// Skills. The eval queue and suite registry are constructed here (ahead
	// of their own route registration below) because publish needs them to
	// enqueue an evaluation for a skill that has a registered suite.
	externalScanner := scan.NewExternalScanner(cfg.ExternalScanCmd, cfg.ExternalScanTimeout)
	if externalScanner != nil {
		log.Info().Str("scanner", externalScanner.Name).Msg("external security scanner enabled")
	}
	skillStore := skill.NewStore(d.Pool)
	evalPool := evalqueue.NewPool(d.Pool)
	qualityStore := quality.NewStore(d.Pool)
	suiteRegistry := evalsuite.NewRegistry(d.Pool, d.Storage)
	skill.RegisterRoutes(api, router, skillStore, d.Storage, skill.RouteOptions{
		External: externalScanner,
		Queue:    evalQueueAdapter{q: evalPool},
		Suites:   evalSuiteAdapter{r: suiteRegistry},

		QualityFloor: cfg.QualityFloor,
	})

	// Sync manifest.
	syncStore := gosync.NewStore(d.Pool)
	huma.Register(api, huma.Operation{
		OperationID: "get-manifest",
		Method:      http.MethodGet,
		Path:        "/api/sync/manifest",
	}, func(ctx context.Context, input *struct{}) (*struct {
		Body []gosync.ManifestEntry
	}, error) {
		entries, err := syncStore.GetManifest(ctx)
		if err != nil {
			return nil, huma.Error500InternalServerError("", err)
		}
		return &struct {
			Body []gosync.ManifestEntry
		}{Body: entries}, nil
	})

	// Analytics.
	analyticsStore := d.AnalyticsStore
	if analyticsStore == nil {
		analyticsStore = analytics.NewStore(d.Pool)
	}
	analytics.RegisterRoutes(api, analyticsStore)

	// Import.
	importStore := skillimport.NewStore(d.Pool)
	importFetcher := skillimport.NewFetcher("https://api.github.com", cfg.GitHubToken)
	skillimport.RegisterRoutes(api, router, importStore, skillStore, d.Storage, importFetcher, skillimport.RouteOptions{
		External:     externalScanner,
		Queue:        evalPool,
		Suites:       suiteRegistry,
		QualityFloor: cfg.QualityFloor,
	})

	// Eval suite registry. suiteRegistry was constructed above, alongside
	// evalPool, so skill.RegisterRoutes could enqueue.
	evalsuite.RegisterRoutes(api, router, suiteRegistry, skillStore)

	// Eval job queue. The server enqueues and ingests; it never holds a
	// Docker socket or an LLM key — those live on the worker.
	evalqueue.RegisterRoutes(api, evalPool, qualityStore, skillStore, suiteRegistry)

	// Read-only quality endpoints: latest score and history.
	quality.RegisterRoutes(api, qualityStore, skillStore)

	return evalPool
}
