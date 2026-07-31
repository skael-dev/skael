package server

import (
	"context"
	"errors"
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
	"github.com/skael-dev/skael/internal/platform"
	"github.com/skael-dev/skael/internal/skill"
	skweb "github.com/skael-dev/skael/web"
)

// evalQueueAdapter adapts an evalqueue.Executor to skill.QueueSubmitter.
// internal/skill cannot import internal/evalqueue directly — evalqueue
// imports internal/skill for its own route wiring, and Go does not allow the
// reverse — so the server, which already imports both, bridges the two.
type evalQueueAdapter struct {
	q evalqueue.Executor
}

func (a evalQueueAdapter) Submit(ctx context.Context, j skill.EvalJobRequest) (string, error) {
	id, err := a.q.Submit(ctx, evalqueue.Job{
		SkillID:     j.SkillID,
		SkillName:   j.SkillName,
		Version:     j.Version,
		SuiteRef:    j.SuiteRef,
		Tier:        j.Tier,
		RequestedBy: j.RequestedBy,
	})
	return string(id), err
}

// evalSuiteAdapter adapts an *evalsuite.Registry to skill.SuiteLookup, for
// the same import-cycle reason as evalQueueAdapter above.
type evalSuiteAdapter struct {
	r *evalsuite.Registry
}

func (a evalSuiteAdapter) LatestForSkill(ctx context.Context, name string) (*skill.SuiteRecord, error) {
	rec, err := a.r.LatestForSkill(ctx, name)
	if err != nil {
		// evalsuite.LatestForSkill reports "no suite registered" as an error
		// wrapping ErrNotFound, not as (nil, nil) — skill.SuiteLookup's
		// contract expects the latter for that case. Translate here so an
		// infrastructure failure (pool exhausted, timeout) still surfaces as
		// an error instead of being indistinguishable from "no suite".
		if errors.Is(err, evalsuite.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if rec == nil {
		return nil, nil
	}
	return &skill.SuiteRecord{Ref: rec.Ref}, nil
}

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

	// 10. Register all /api/* routes (health, capabilities, readiness, auth,
	// skills, sync manifest, analytics, import, eval suites, eval queue,
	// quality). This is the same registration path `skael-server --openapi`
	// uses, so the generated spec cannot drift from what the real server
	// serves — see internal/server/routes.go.
	analyticsStore := analytics.NewStore(b.pool)
	RegisterAPIRoutes(api, router, RegisterAPIDeps{
		Pool:           b.pool,
		Config:         cfg,
		SessionManager: sessionManager,
		UserStore:      userStore,
		KeyStore:       keyStore,
		Storage:        storage,
		Caps:           b.caps,
		AnalyticsStore: analyticsStore,
	})

	// 10a. Run event retention cleanup on startup.
	if cfg.EventRetentionDays > 0 {
		deleted, err := analyticsStore.CleanupOldEvents(context.Background(), cfg.EventRetentionDays)
		if err != nil {
			log.Warn().Err(err).Msg("event retention cleanup failed")
		} else if deleted > 0 {
			log.Info().Int64("deleted", deleted).Int("retention_days", cfg.EventRetentionDays).Msg("event retention cleanup complete")
		}
	}

	// 11. Register extra routes from enterprise plugins.
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
