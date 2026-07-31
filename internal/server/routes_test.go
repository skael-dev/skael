package server_test

import (
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/skael-dev/skael/internal/server"
)

// TestRegisterAPIRoutes_CoversEveryGroup guards against the OpenAPI spec
// (skael-server --openapi) silently falling behind the real server: both
// call server.RegisterAPIRoutes, so this test exercises exactly what spec
// generation produces. If a future route group is registered directly in
// Build() instead of through RegisterAPIRoutes, it will show up here as
// missing and this test will fail.
func TestRegisterAPIRoutes_CoversEveryGroup(t *testing.T) {
	router := chi.NewMux()
	config := huma.DefaultConfig("Skael API", "1.0.0")
	api := humachi.New(router, config)

	server.RegisterAPIRoutes(api, router, server.RegisterAPIDeps{})

	paths := api.OpenAPI().Paths
	for _, want := range []string{
		"/api/health",
		"/api/health/ready",
		"/api/capabilities",
		"/api/sync/manifest",
		"/api/eval/jobs/claim",
		"/api/eval/jobs/{id}",
		"/api/eval/suites",
		"/api/skills/{name}/quality",
		"/api/skills/{name}/quality/history",
	} {
		require.Containsf(t, paths, want, "route group missing from RegisterAPIRoutes: %s", want)
	}
}
