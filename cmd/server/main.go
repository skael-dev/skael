package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"github.com/skael-dev/skael/internal/auth"
	"github.com/skael-dev/skael/internal/platform"
	"github.com/skael-dev/skael/internal/server"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	platform.InitLogger()

	// Stamped by the release build via -ldflags -X. Logged so an operator
	// reading logs can tell which build is actually running.
	log.Info().
		Str("version", version).
		Str("commit", commit).
		Str("built", date).
		Msg("skael-server starting")

	for _, arg := range os.Args[1:] {
		if arg == "--openapi" {
			printOpenAPISpec()
			os.Exit(0)
		}
	}

	if len(os.Args) > 1 && os.Args[1] == "reset-password" {
		resetPassword(os.Args[2:])
		return
	}

	ctx := context.Background()

	cfg, err := platform.LoadConfig()
	if err != nil {
		log.Fatal().Err(err).Msg("configuration error")
	}

	pool, err := platform.NewPool(ctx, cfg.DatabaseURL, &platform.PoolConfig{
		MaxConns:          cfg.DBMaxConns,
		MinConns:          cfg.DBMinConns,
		MaxConnLifetime:   cfg.DBMaxConnLifetime,
		MaxConnIdleTime:   cfg.DBMaxConnIdleTime,
		HealthCheckPeriod: cfg.DBHealthCheckPeriod,
	})
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

func resetPassword(args []string) {
	fs := flag.NewFlagSet("reset-password", flag.ExitOnError)
	email := fs.String("email", "", "email of the user to reset")
	// ExitOnError: Parse exits the process on a bad flag rather than returning.
	_ = fs.Parse(args)

	if *email == "" {
		fmt.Fprintln(os.Stderr, "usage: skael-server reset-password --email <email>")
		os.Exit(1)
	}

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL is required")
		os.Exit(1)
	}

	ctx := context.Background()
	pool, err := platform.NewPool(ctx, dbURL, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "database connection error: %v\n", err)
		os.Exit(1)
	}
	defer pool.Close()

	userStore := auth.NewUserStore(pool)
	row, err := userStore.GetByEmail(ctx, *email)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lookup error: %v\n", err)
		os.Exit(1)
	}
	if row == nil {
		fmt.Fprintf(os.Stderr, "user not found: %s\n", *email)
		os.Exit(1)
	}

	tempPass, err := auth.GenerateTemporaryPassword()
	if err != nil {
		fmt.Fprintf(os.Stderr, "generate password error: %v\n", err)
		os.Exit(1)
	}

	hash, err := auth.HashPassword(tempPass)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hash password error: %v\n", err)
		os.Exit(1)
	}

	if err := userStore.UpdatePassword(ctx, row.ID, hash); err != nil {
		fmt.Fprintf(os.Stderr, "update password error: %v\n", err)
		os.Exit(1)
	}
	if err := userStore.SetResetRequired(ctx, row.ID, true); err != nil {
		fmt.Fprintf(os.Stderr, "set reset flag error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(tempPass)
}

// printOpenAPISpec builds a throwaway Huma API with no live database or
// storage and registers every route through server.RegisterAPIRoutes — the
// same function Builder.Build uses for the real server — so the generated
// spec (web/openapi.json, and the TS SDK generated from it) cannot drift
// from what the server actually serves. All RegisterAPIDeps fields are left
// nil/zero: route registration only needs types, not live data.
func printOpenAPISpec() {
	router := chi.NewMux()
	config := huma.DefaultConfig("Skael API", "1.0.0")
	api := humachi.New(router, config)

	server.RegisterAPIRoutes(api, router, server.RegisterAPIDeps{})

	spec, err := json.MarshalIndent(api.OpenAPI(), "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "openapi marshal error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(spec))
}
