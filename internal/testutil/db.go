// Package testutil provides the Postgres a DB-backed test runs against.
package testutil

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/skael-dev/skael/internal/platform"
)

// One container per test binary, not one per test. A container start plus a
// full migration run costs seconds; CREATE DATABASE ... TEMPLATE is a file
// copy and costs milliseconds. Each test still gets a pristine migrated
// database of its own, so nothing about isolation changes — 144 call sites
// used to mean 144 Postgres containers.
//
// The container is left to the testcontainers reaper, which terminates it when
// the test binary exits. A t.Cleanup would tear it down after the first test.
var (
	shared struct {
		adminDSN string
		err      error
	}
	sharedOnce sync.Once
	dbSeq      atomic.Int64
)

// templateDB holds the fully migrated schema every test database is copied
// from. Migrated exactly once per binary.
const templateDB = "skael_template"

func sharedContainer(t *testing.T) string {
	t.Helper()
	sharedOnce.Do(func() { shared.adminDSN, shared.err = startShared() })
	if shared.err != nil {
		t.Fatalf("testutil: %v", shared.err)
	}
	return shared.adminDSN
}

func startShared() (string, error) {
	ctx := context.Background()
	c, err := postgres.Run(ctx,
		"postgres:17",
		postgres.WithDatabase("skael_test"),
		postgres.WithUsername("skael"),
		postgres.WithPassword("skael"),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		return "", fmt.Errorf("start postgres container: %w", err)
	}
	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return "", fmt.Errorf("get connection string: %w", err)
	}

	// Migrate the template through a pool that is closed again immediately:
	// CREATE DATABASE ... TEMPLATE refuses while anything is connected to the
	// template.
	if err := exec(ctx, dsn, "CREATE DATABASE "+templateDB); err != nil {
		return "", err
	}
	tmplPool, err := platform.NewPool(ctx, dsnFor(dsn, templateDB), nil)
	if err != nil {
		return "", fmt.Errorf("connect to template: %w", err)
	}
	migErr := platform.RunMigrations(ctx, tmplPool)
	tmplPool.Close()
	if migErr != nil {
		return "", fmt.Errorf("migrate template: %w", migErr)
	}
	return dsn, nil
}

// SetupTestDB returns a pool onto a fresh, fully migrated database. The
// database is dropped when the test finishes.
func SetupTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return newDatabase(t, sharedContainer(t), templateDB)
}

// SetupTestDBAtVersion is SetupTestDB stopped at a specific migration version,
// so a migration can be exercised against a populated older database rather
// than against a schema that already contains it.
func SetupTestDBAtVersion(t *testing.T, version int64) *pgxpool.Pool {
	t.Helper()
	pool := newDatabase(t, sharedContainer(t), "")
	db := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { _ = db.Close() })
	if err := platform.MigrateUpTo(db, version); err != nil {
		t.Fatalf("migrate up to %d: %v", version, err)
	}
	return pool
}

// newDatabase creates a database from template (empty when template is "") and
// returns a pool onto it.
func newDatabase(t *testing.T, adminDSN, template string) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	name := fmt.Sprintf("skael_t%d", dbSeq.Add(1))

	create := "CREATE DATABASE " + name
	if template != "" {
		create += " TEMPLATE " + template
	}
	if err := exec(ctx, adminDSN, create); err != nil {
		t.Fatalf("testutil: %v", err)
	}

	pool, err := platform.NewPool(ctx, dsnFor(adminDSN, name), nil)
	if err != nil {
		t.Fatalf("testutil: connect to %s: %v", name, err)
	}
	t.Cleanup(func() {
		pool.Close()
		// Best effort: the container goes away with the binary anyway, and a
		// failed drop must not fail a passing test.
		_ = exec(context.Background(), adminDSN, "DROP DATABASE IF EXISTS "+name)
	})
	return pool
}

func exec(ctx context.Context, dsn, sql string) error {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, sql); err != nil {
		return fmt.Errorf("%s: %w", sql, err)
	}
	return nil
}

// dsnFor swaps the database name in a connection string.
func dsnFor(dsn, database string) string {
	head, tail, found := strings.Cut(dsn, "?")
	slash := strings.LastIndex(head, "/")
	head = head[:slash+1] + database
	if !found {
		return head
	}
	return head + "?" + tail
}
