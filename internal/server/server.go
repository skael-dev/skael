package server

import (
	"context"
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
	"github.com/rs/zerolog/log"

	"github.com/skael-dev/skael/internal/analytics"
	"github.com/skael-dev/skael/internal/auth"
	skillimport "github.com/skael-dev/skael/internal/import"
	"github.com/skael-dev/skael/internal/platform"
	"github.com/skael-dev/skael/internal/skill"
	gosync "github.com/skael-dev/skael/internal/sync"
	skweb "github.com/skael-dev/skael/web"
)

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

	// 4. Initialize session manager.
	sessionManager := scs.New()
	sessionManager.Store = pgxstore.NewWithCleanupInterval(b.pool, 30*time.Minute)
	sessionManager.Cookie.Name = "skael_session"
	sessionManager.Cookie.HttpOnly = true
	sessionManager.Cookie.SameSite = http.SameSiteLaxMode
	// Secure defaults to false for self-hosted ease (HTTP). Set COOKIE_SECURE=true
	// in production behind a TLS-terminating reverse proxy.
	sessionManager.Cookie.Secure = os.Getenv("COOKIE_SECURE") == "true"
	sessionManager.Lifetime = 7 * 24 * time.Hour

	// 5. Create storage.
	storage, err := platform.NewStorage(cfg.StoragePath)
	if err != nil {
		return nil, fmt.Errorf("server.Build: storage: %w", err)
	}

	// 6. Create auth stores.
	userStore := auth.NewUserStore(b.pool)
	keyStore := auth.NewKeyStore(b.pool)

	// 7. Create chi router with middleware.
	router := chi.NewMux()
	router.Use(middleware.Recoverer)
	router.Use(middleware.RealIP)
	router.Use(platform.RequestLogger)
	router.Use(sessionManager.LoadAndSave)
	router.Use(auth.Middleware(sessionManager, userStore, keyStore, cfg.APIKey))

	// 8. Enforce body size limit before Huma buffers the request body.
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, 10<<20) // 10MB
			next.ServeHTTP(w, r)
		})
	})

	// 9. Create Huma API.
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

	// 11. Register auth routes.
	auth.RegisterRoutes(api, sessionManager, userStore, keyStore, cfg.DisableSignup)

	// 12. Register skill routes.
	skillStore := skill.NewStore(b.pool)
	skill.RegisterRoutes(api, router, skillStore, storage)

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

	// 15. Register import routes.
	importStore := skillimport.NewStore(b.pool)
	importFetcher := skillimport.NewFetcher("https://api.github.com", cfg.GitHubToken)
	skillimport.RegisterRoutes(api, router, importStore, skillStore, storage, importFetcher)

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
