package platform_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/skael-dev/skael/internal/platform"
	"github.com/skael-dev/skael/internal/testutil"
)

// A candidate stored before reuse existed measured itself, so it must read as
// not reused, and its report must survive.
func TestMigration027KeepsExistingCandidatesAsMeasured(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupTestDBAtVersion(t, 26)

	var skillID, contestID string
	if err := pool.QueryRow(ctx, `INSERT INTO skills (name) VALUES ('legacy-027') RETURNING id`).Scan(&skillID); err != nil {
		t.Fatalf("seed skill: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO contests (suite_ref, suite_provenance) VALUES ('ref-027', 'authored') RETURNING id`).Scan(&contestID); err != nil {
		t.Fatalf("seed contest: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO contest_candidates (contest_id, position, skill_id, skill_name, version, label, report_json)
		VALUES ($1, 0, $2, 'legacy-027', 1, 'legacy-027@1', '{"tier":"full"}')`, contestID, skillID); err != nil {
		t.Fatalf("seed candidate: %v", err)
	}

	db := stdlib.OpenDBFromPool(pool)
	defer db.Close() //nolint:errcheck
	if err := platform.MigrateUpTo(db, 27); err != nil {
		t.Fatalf("migrate to 27: %v", err)
	}

	var reused *string
	var tier string
	if err := pool.QueryRow(ctx, `
		SELECT reused_from::text, report_json->>'tier' FROM contest_candidates WHERE contest_id = $1`,
		contestID).Scan(&reused, &tier); err != nil {
		t.Fatalf("read candidate: %v", err)
	}
	if reused != nil {
		t.Errorf("reused_from = %q, want NULL for a candidate that measured itself", *reused)
	}
	if tier != "full" {
		t.Errorf("report lost in the migration: tier = %q", tier)
	}
}
