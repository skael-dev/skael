package platform_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/skael-dev/skael/internal/platform"
	"github.com/skael-dev/skael/internal/testutil"
)

// The default must be exercised against a database that already holds a suite
// row, built at 17. A fresh database has no rows and would pass with the
// DEFAULT clause deleted.
func TestMigration018DefaultsExistingSuitesToAuthored(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupTestDBAtVersion(t, 17)

	if _, err := pool.Exec(ctx, `
		INSERT INTO eval_suites (ref, skill_name, archive_path, task_count, checks, spec_version, uploaded_by)
		VALUES ('ref-018', 'legacy-skill', 'suites/ref-018.tar.gz', 3, '[]'::jsonb, 1, 'someone@example.com')`); err != nil {
		t.Fatalf("seed pre-migration row: %v", err)
	}

	db := stdlib.OpenDBFromPool(pool)
	defer db.Close() //nolint:errcheck
	if err := platform.MigrateUpTo(db, 18); err != nil {
		t.Fatalf("migrate to 18: %v", err)
	}

	// An upgrade must not reclassify history: a suite that predates the
	// column is authored, not derived and not NULL.
	var origin string
	if err := pool.QueryRow(ctx, `SELECT origin FROM eval_suites WHERE ref = 'ref-018'`).Scan(&origin); err != nil {
		t.Fatalf("read origin: %v", err)
	}
	if origin != "authored" {
		t.Fatalf("pre-existing suite got origin %q, want %q", origin, "authored")
	}
}
