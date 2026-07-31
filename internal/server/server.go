package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/alexedwards/scs/pgxstore"
	"github.com/alexedwards/scs/v2"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
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
	skweb "github.com/skael-dev/skael/web"
)

// InstallEdgeMiddleware registers every middleware a request passes through
// before it reaches session handling, authentication, or any route: security
// headers, request ID, CORS, panic recovery, request logging, and rate
// limiting (plus Prometheus instrumentation unless METRICS_ENABLED=false).
//
// Order matters here, and it is the reason this lives in its own function
// rather than inline in Build: the rate limiter and the request logger both
// need to agree on who a request came from, so anything that establishes the
// client identity must run before either of them. Exporting it lets tests
// exercise the real chain — in the real order — without a database.
//
// It returns mountMetrics, which registers the /metrics endpoint. Callers must
// call it only after adding every middleware of their own: chi panics if Use
// is called once any route exists on the mux, and route registration order has
// no effect on which middleware a route runs through.
func InstallEdgeMiddleware(router chi.Router, cfg *platform.Config, cookieSecure bool) (mountMetrics func()) {
	router.Use(platform.SecurityHeaders(cookieSecure))
	router.Use(platform.RequestID)

	if cfg.CORSOrigins != "" {
		origins := strings.Split(cfg.CORSOrigins, ",")
		for i := range origins {
			origins[i] = strings.TrimSpace(origins[i])
		}
		router.Use(cors.Handler(cors.Options{
			AllowedOrigins:   origins,
			AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
			AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-API-Key", "X-Request-ID"},
			ExposedHeaders:   []string{"X-Request-ID"},
			AllowCredentials: true,
			MaxAge:           300,
		}))
	}

	router.Use(middleware.Recoverer)
	router.Use(platform.ClientIP(platform.ParseTrustedProxies(cfg.TrustedProxies)))
	router.Use(platform.RequestLogger)

	router.Use(platform.ClassifiedRateLimiter(platform.RateLimitConfig{
		Auth:   cfg.RateLimitAuth,
		Events: cfg.RateLimitEvents,
		Read:   cfg.RateLimitRead,
		Write:  cfg.RateLimitWrite,
		Suites: cfg.RateLimitSuites,
	}))

	if os.Getenv("METRICS_ENABLED") != "false" {
		router.Use(platform.MetricsMiddleware)
		return func() { router.Get("/metrics", promhttp.Handler().ServeHTTP) }
	}
	return func() {}
}

// Server wraps the assembled HTTP handler and configuration needed to start
// listening. Build() produces a Server; ListenAndServe() runs it.
type Server struct {
	// Handler is the fully assembled Chi router, ready to be passed to
	// http.Server or used in tests with httptest.NewServer.
	Handler http.Handler

	listenAddr string
}

// Build assembles all server components from the builder and returns a Server
// ready to serve. It creates the session manager, storage, auth stores, router,
// Huma API, all routes, and the embedded SPA mount. It does NOT call
// ListenAndServe — that's a separate method on Server so callers can inject
// test transports or configure TLS before starting.
func (b *Builder) Build() (*Server, error) {
	if b.pool == nil {
		return nil, fmt.Errorf("server.Build: pool is required")
	}
	if b.config == nil {
		return nil, fmt.Errorf("server.Build: config is required")
	}

	cfg := b.config

	// Startup warnings.
	cookieSecure := os.Getenv("COOKIE_SECURE") == "true"
	if !cookieSecure {
		log.Warn().Msg("COOKIE_SECURE is not set")
	}
	if os.Getenv("DISABLE_SIGNUP") != "true" {
		log.Warn().Msg("DISABLE_SIGNUP is not set")
	}

	// 4. Initialize session manager.
	sessionManager := scs.New()
	sessionManager.Store = pgxstore.NewWithCleanupInterval(b.pool, 30*time.Minute)
	sessionManager.Cookie.Name = "skael_session"
	sessionManager.Cookie.HttpOnly = true
	sessionManager.Cookie.SameSite = http.SameSiteLaxMode
	sessionManager.Cookie.Secure = cookieSecure
	sessionManager.Lifetime = 7 * 24 * time.Hour

	// 5. Create storage (local filesystem or S3, per STORAGE_PATH).
	storage, err := platform.NewStorageFromConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("server.Build: storage: %w", err)
	}

	// 6. Create auth stores.
	userStore := auth.NewUserStore(b.pool)
	keyStore := auth.NewKeyStore(b.pool)

	// 7. Create chi router with middleware.
	router := chi.NewMux()
	mountMetrics := InstallEdgeMiddleware(router, cfg, cookieSecure)

	router.Use(sessionManager.LoadAndSave)
	router.Use(auth.Middleware(sessionManager, userStore, keyStore))

	// 8. Enforce body size limit before Huma buffers the request body.
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, 10<<20) // 10MB
			next.ServeHTTP(w, r)
		})
	})

	// 9. All middleware is registered; routes may now be mounted.
	mountMetrics()

	// 9a. Create Huma API.
	config := huma.DefaultConfig("Skael API", "1.0.0")
	api := humachi.New(router, config)

	// 10. Register health endpoint (auth middleware skips /api/health).
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

	// 10a. Register capabilities endpoint.
	b.caps.Register(api)

	// 10b. Readiness: verifies DB and storage connectivity. Liveness stays on
	// /api/health (static) so orchestrators don't restart pods on DB blips.
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

		checks, ready := readinessChecks(checkCtx, b.pool, storage)
		if !ready {
			detail, _ := json.Marshal(checks)
			return nil, huma.NewError(http.StatusServiceUnavailable, "not ready", fmt.Errorf("%s", detail))
		}
		out := &struct{ Body readyBody }{}
		out.Body.Status = "ready"
		out.Body.Checks = checks
		return out, nil
	})

	// 11. Register auth routes.
	auth.RegisterRoutes(api, sessionManager, userStore, keyStore, cfg.DisableSignup)

	// 12. Register skill routes. An opt-in external scanner (EXTERNAL_SCAN_CMD)
	// is merged into the publish/import security scan when configured.
	externalScanner := scan.NewExternalScanner(cfg.ExternalScanCmd, cfg.ExternalScanTimeout)
	if externalScanner != nil {
		log.Info().Str("scanner", externalScanner.Name).Msg("external security scanner enabled")
	}
	skillStore := skill.NewStore(b.pool)
	skill.RegisterRoutes(api, router, skillStore, storage, externalScanner)

	// 13. Register sync manifest route.
	syncStore := gosync.NewStore(b.pool)
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

	// 14. Register analytics routes.
	analyticsStore := analytics.NewStore(b.pool)
	analytics.RegisterRoutes(api, analyticsStore)

	// 14a. Run event retention cleanup on startup.
	if cfg.EventRetentionDays > 0 {
		deleted, err := analyticsStore.CleanupOldEvents(context.Background(), cfg.EventRetentionDays)
		if err != nil {
			log.Warn().Err(err).Msg("event retention cleanup failed")
		} else if deleted > 0 {
			log.Info().Int64("deleted", deleted).Int("retention_days", cfg.EventRetentionDays).Msg("event retention cleanup complete")
		}
	}

	// 15. Register import routes.
	importStore := skillimport.NewStore(b.pool)
	importFetcher := skillimport.NewFetcher("https://api.github.com", cfg.GitHubToken)
	skillimport.RegisterRoutes(api, router, importStore, skillStore, storage, importFetcher, externalScanner)

	// 15a. Register eval suite registry routes.
	suiteRegistry := evalsuite.NewRegistry(b.pool, storage)
	evalsuite.RegisterRoutes(api, router, suiteRegistry, skillStore)

	// 15b. Register the eval job queue. The server enqueues and ingests; it
	// never holds a Docker socket or an LLM key — those live on the worker.
	evalPool := evalqueue.NewPool(b.pool)
	qualityStore := quality.NewStore(b.pool)
	evalqueue.RegisterRoutes(api, evalPool, qualityStore, skillStore)

	// 16. Register extra routes from enterprise plugins.
	for _, reg := range b.extraRoutes {
		reg(api, router, b.pool)
	}

	// 17. Mount embedded SPA — catch-all after all /api/* routes.
	spaFS, err := fs.Sub(skweb.Assets, "dist")
	if err != nil {
		return nil, fmt.Errorf("server.Build: embedded SPA: %w", err)
	}
	fileServer := http.FileServer(http.FS(spaFS))

	router.Get("/*", func(w http.ResponseWriter, r *http.Request) {
		// Try to open the requested file directly.
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		f, err := spaFS.Open(path)
		if err == nil {
			f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}
		// Fall back to index.html for client-side routing.
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})

	return &Server{
		Handler:    router,
		listenAddr: cfg.ListenAddr,
	}, nil
}

// ListenAndServe starts the HTTP server and blocks until a SIGINT or SIGTERM
// is received, then performs a graceful shutdown with a 10-second timeout.
func (s *Server) ListenAndServe() error {
	httpServer := &http.Server{
		Addr:              s.listenAddr,
		Handler:           s.Handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error().Err(err).Msg("server error")
			os.Exit(1)
		}
	}()

	log.Info().Str("addr", s.listenAddr).Msg("skael-server listening")
	<-sigCtx.Done()
	log.Info().Msg("shutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	return nil
}
