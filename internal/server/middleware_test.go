package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skael-dev/skael/internal/platform"
)

// TestBuild_AssemblesWithMetricsEnabled guards the assembly order itself. Chi
// panics if Use is called once any route exists on the mux, so mounting
// /metrics in the middle of the middleware stack took the server down at boot
// for every deployment that had not set METRICS_ENABLED=false. No database is
// touched before the router is built, so a pool that never connects is enough
// to exercise it.
func TestBuild_AssemblesWithMetricsEnabled(t *testing.T) {
	t.Setenv("METRICS_ENABLED", "true")

	pool, err := pgxpool.New(context.Background(), "postgres://u:p@127.0.0.1:1/nowhere?sslmode=disable")
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	srv, err := NewBuilder(pool, &platform.Config{StoragePath: t.TempDir(), ListenAddr: ":0"}).Build()
	require.NoError(t, err)
	require.NotNil(t, srv.Handler)

	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	assert.Equal(t, http.StatusOK, rec.Code)
}
