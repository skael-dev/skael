package platform_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/skael-dev/skael/internal/platform"
	"github.com/skael-dev/skael/internal/testutil"
)

// The backfill must be exercised against a database that already holds suite
// rows, built at 19. A fresh database has no rows, so the UPDATE could be
// deleted and this would still pass.
//
// Every suite recorded derived before this column existed was a worker's.
// Without the backfill each one loses its void tolerance on the next re-run,
// and the run refuses on tasks that were fine the first time.
func TestMigration020BackfillsDerivedSuitesAsMachineGenerated(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupTestDBAtVersion(t, 19)

	if _, err := pool.Exec(ctx, `
		INSERT INTO eval_suites (ref, skill_name, archive_path, task_count, checks, spec_version, uploaded_by, origin)
		VALUES ('ref-020-derived', 'legacy-skill', 'suites/a.tar.gz', 3, '[]'::jsonb, 1, 'worker@example.com', 'derived'),
		       ('ref-020-authored', 'legacy-skill', 'suites/b.tar.gz', 3, '[]'::jsonb, 1, 'author@example.com', 'authored')`); err != nil {
		t.Fatalf("seed pre-migration rows: %v", err)
	}

	db := stdlib.OpenDBFromPool(pool)
	defer db.Close() //nolint:errcheck
	if err := platform.MigrateUpTo(db, 20); err != nil {
		t.Fatalf("migrate to 20: %v", err)
	}

	var derived, authored bool
	if err := pool.QueryRow(ctx,
		`SELECT machine_generated FROM eval_suites WHERE ref = 'ref-020-derived'`).Scan(&derived); err != nil {
		t.Fatalf("read the derived row: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT machine_generated FROM eval_suites WHERE ref = 'ref-020-authored'`).Scan(&authored); err != nil {
		t.Fatalf("read the authored row: %v", err)
	}
	if !derived {
		t.Error("a suite already recorded derived was not backfilled, so its re-run refuses void tasks")
	}
	if authored {
		t.Error("an authored suite was backfilled machine generated, which excuses its void tasks")
	}
}
