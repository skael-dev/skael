package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/alexedwards/scs/pgxstore"
	"github.com/alexedwards/scs/v2"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/skael-dev/skael/internal/analytics"
	"github.com/skael-dev/skael/internal/auth"
	"github.com/skael-dev/skael/internal/platform"
	"github.com/skael-dev/skael/internal/skill"
	gosync "github.com/skael-dev/skael/internal/sync"
	skweb "github.com/skael-dev/skael/web"
)

func main() {
	// --openapi: print the OpenAPI spec and exit (used at build time by the SPA).
	for _, arg := range os.Args[1:] {
		if arg == "--openapi" {
			router := chi.NewMux()
			config := huma.DefaultConfig("Skael API", "1.0.0")
			api := humachi.New(router, config)

			// Register all operations so the spec is complete. Handlers are
			// never called here, so nil stores/storage are safe.
			auth.RegisterRoutes(api, nil, nil, nil, false)
			skill.RegisterRoutes(api, router, nil, nil)
			analytics.RegisterRoutes(api, nil)

			huma.Register(api, huma.Operation{
				OperationID: "get-manifest",
				Method:      http.MethodGet,
				Path:        "/api/sync/manifest",
			}, func(ctx context.Context, input *struct{}) (*struct {
				Body []gosync.ManifestEntry
			}, error) {
				return nil, nil
			})

			huma.Register(api, huma.Operation{
				OperationID: "health",
				Method:      http.MethodGet,
				Path:        "/api/health",
			}, func(ctx context.Context, input *struct{}) (*struct {
				Body struct {
					Status string `json:"status"`
				}
			}, error) {
				return nil, nil
			})

			spec, err := json.MarshalIndent(api.OpenAPI(), "", "  ")
			if err != nil {
				fmt.Fprintf(os.Stderr, "openapi marshal error: %v\n", err)
				os.Exit(1)
			}
			fmt.Println(string(spec))
			os.Exit(0)
		}
	}

	// 1. Load config.
	cfg, err := platform.LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "configuration error: %v\n", err)
		os.Exit(1)
	}

	// 2. Connect to database.
	ctx := context.Background()
	pool, err := platform.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "database connection error: %v\n", err)
		os.Exit(1)
	}
	defer pool.Close()

	// 3. Run migrations.
	if err := platform.RunMigrations(ctx, pool); err != nil {
		fmt.Fprintf(os.Stderr, "migration error: %v\n", err)
		os.Exit(1)
	}

	// 4. Initialize session manager.
	sessionManager := scs.New()
	sessionManager.Store = pgxstore.NewWithCleanupInterval(pool, 30*time.Minute)
	sessionManager.Cookie.Name = "skael_session"
	sessionManager.Cookie.HttpOnly = true
	sessionManager.Cookie.SameSite = http.SameSiteLaxMode
	sessionManager.Lifetime = 7 * 24 * time.Hour

	// 5. Create storage.
	storage, err := platform.NewStorage(cfg.StoragePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "storage error: %v\n", err)
		os.Exit(1)
	}

	// 6. Create auth stores.
	userStore := auth.NewUserStore(pool)
	keyStore := auth.NewKeyStore(pool)

	// 7. Create chi router with middleware.
	router := chi.NewMux()
	router.Use(middleware.Recoverer)
	router.Use(middleware.RealIP)
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

	// 11. Register auth routes.
	auth.RegisterRoutes(api, sessionManager, userStore, keyStore, cfg.DisableSignup)

	// 12. Register skill routes.
	skillStore := skill.NewStore(pool)
	skill.RegisterRoutes(api, router, skillStore, storage)

	// 13. Register sync manifest route.
	syncStore := gosync.NewStore(pool)
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
	analyticsStore := analytics.NewStore(pool)
	analytics.RegisterRoutes(api, analyticsStore)

	// 15. Mount embedded SPA — catch-all after all /api/* routes.
	spaFS, err := fs.Sub(skweb.Assets, "dist")
	if err != nil {
		fmt.Fprintf(os.Stderr, "embedded SPA error: %v\n", err)
		os.Exit(1)
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

	// 16. Start server.
	fmt.Printf("skael-server listening on %s\n", cfg.ListenAddr)
	server := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	if err := server.ListenAndServe(); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}
