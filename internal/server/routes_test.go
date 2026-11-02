package server_test

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/skael-dev/skael/internal/auth"
	"github.com/skael-dev/skael/internal/server"
	"github.com/skael-dev/skael/internal/webhooks"
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

// A route missing from the table is unreachable by a restricted key, which is
// safe, but a new route must be a decision rather than an accident.
func TestEveryRouteHasAScopeTableEntry(t *testing.T) {
	router := chi.NewMux()
	api := humachi.New(router, huma.DefaultConfig("Skael API", "1.0.0"))
	server.RegisterAPIRoutes(api, router, server.RegisterAPIDeps{})

	known := map[string]bool{}
	for _, r := range auth.RouteRules() {
		known[r.Method+" "+r.Pattern] = true
	}
	err := chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if !strings.HasPrefix(route, "/api/") && route != "/mcp" {
			return nil
		}
		// River UI serves every method under one prefix. It has no rule, so
		// matchRule finds nothing and a restricted key gets 403.
		if strings.HasPrefix(route, server.RiverUIPrefix) {
			return nil
		}
		if !known[method+" "+route] {
			t.Errorf("no entry in auth.routeRules for %s %s", method, route)
		}
		delete(known, method+" "+route)
		return nil
	})
	require.NoError(t, err)
	for stale := range known {
		t.Errorf("auth.routeRules names a route that does not exist: %s", stale)
	}
}

// River UI shows job arguments to admins, so an argument that grew a payload or
// a secret would publish it there.
func TestJobArgsHoldIDsOnly(t *testing.T) {
	for _, args := range []any{webhooks.DeliveryArgs{}} {
		typ := reflect.TypeOf(args)
		for i := range typ.NumField() {
			field := typ.Field(i)
			require.Truef(t, field.Type.Kind() == reflect.String && strings.HasSuffix(field.Name, "ID"),
				"%s.%s is not an id", typ.Name(), field.Name)
		}
	}
}
