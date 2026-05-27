package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"github.com/skael-dev/skael/internal/analytics"
	"github.com/skael-dev/skael/internal/auth"
	skillimport "github.com/skael-dev/skael/internal/import"
	"github.com/skael-dev/skael/internal/platform"
	"github.com/skael-dev/skael/internal/server"
	"github.com/skael-dev/skael/internal/skill"
	gosync "github.com/skael-dev/skael/internal/sync"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	platform.InitLogger()

	for _, arg := range os.Args[1:] {
		if arg == "--openapi" {
			printOpenAPISpec()
			os.Exit(0)
		}
	}

	ctx := context.Background()

	cfg, err := platform.LoadConfig()
	if err != nil {
		log.Fatal().Err(err).Msg("configuration error")
	}

	pool, err := platform.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatal().Err(err).Msg("database connection error")
	}
	defer pool.Close()

	if err := platform.RunMigrations(ctx, pool); err != nil {
		log.Fatal().Err(err).Msg("migration error")
	}

	srv, err := server.NewBuilder(pool, cfg).Build()
	if err != nil {
		log.Fatal().Err(err).Msg("server build error")
	}

	if err := srv.ListenAndServe(); err != nil {
		log.Fatal().Err(err).Msg("server error")
	}
}

func printOpenAPISpec() {
	router := chi.NewMux()
	config := huma.DefaultConfig("Skael API", "1.0.0")
	api := humachi.New(router, config)

	auth.RegisterRoutes(api, nil, nil, nil, false)
	skill.RegisterRoutes(api, router, nil, nil)
	analytics.RegisterRoutes(api, nil)
	skillimport.RegisterRoutes(api, router, nil, nil, nil, nil)

	huma.Register(api, huma.Operation{
		OperationID: "get-manifest",
		Method:      http.MethodGet,
		Path:        "/api/sync/manifest",
	}, func(_ context.Context, _ *struct{}) (*struct {
		Body []gosync.ManifestEntry
	}, error) {
		return nil, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "health",
		Method:      http.MethodGet,
		Path:        "/api/health",
	}, func(_ context.Context, _ *struct{}) (*struct {
		Body struct {
			Status string `json:"status"`
		}
	}, error) {
		return nil, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-capabilities",
		Method:      http.MethodGet,
		Path:        "/api/capabilities",
	}, func(_ context.Context, _ *struct{}) (*struct {
		Body server.CapabilitiesResponse
	}, error) {
		return nil, nil
	})

	spec, err := json.MarshalIndent(api.OpenAPI(), "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "openapi marshal error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(spec))
}
