package testutil

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/skael-dev/skael/internal/platform"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

// SetupTestDB starts an ephemeral Postgres 17 container, runs all migrations,
// and returns a ready pgxpool.Pool. The container and pool are closed
// automatically when the test finishes.
func SetupTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	// Start a postgres:17 container.
	pgContainer, err := postgres.Run(ctx,
		"postgres:17",
		postgres.WithDatabase("skael_test"),
		postgres.WithUsername("skael"),
		postgres.WithPassword("skael"),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() {
		if err := pgContainer.Terminate(ctx); err != nil {
			t.Logf("terminate postgres container: %v", err)
		}
	})

	// Obtain the connection string from the container.
	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("get connection string: %v", err)
	}

	// Create the pgxpool.
	pool, err := platform.NewPool(ctx, connStr)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	t.Cleanup(pool.Close)

	// Apply migrations.
	if err := platform.RunMigrations(ctx, pool); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	return pool
}
