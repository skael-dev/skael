package platform_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/skael-dev/skael/internal/platform"
	"github.com/skael-dev/skael/internal/testutil"
)

// The backfill must be exercised against a database that already contains a
// held version, built at 16. A fresh database has no rows and would pass with
// the backfill statement deleted.
func TestMigration017BackfillsHeldVersions(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupTestDBAtVersion(t, 16)

	var skillID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO skills (name, description) VALUES ('legacy:held', 'x') RETURNING id`).
		Scan(&skillID); err != nil {
		t.Fatalf("seed skill: %v", err)
	}
	for _, tc := range []struct {
		version int
		state   string
	}{
		{1, "released"},
		{2, "needs_review"},
		{3, "rejected"},
	} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO skill_versions (skill_id, version, archive_path, checksum, gate_state)
			VALUES ($1, $2, 'a.tar.gz', 'deadbeef', $3)`, skillID, tc.version, tc.state); err != nil {
			t.Fatalf("seed version %d: %v", tc.version, err)
		}
	}

	db := stdlib.OpenDBFromPool(pool)
	defer db.Close() //nolint:errcheck
	if err := platform.MigrateUpTo(db, 17); err != nil {
		t.Fatalf("migrate to 17: %v", err)
	}

	for _, tc := range []struct {
		version int
		want    []string
	}{
		{1, []string{}},
		{2, []string{"scan"}},
		{3, []string{}},
	} {
		var got []string
		if err := pool.QueryRow(ctx,
			`SELECT hold_reasons FROM skill_versions WHERE skill_id = $1 AND version = $2`,
			skillID, tc.version).Scan(&got); err != nil {
			t.Fatalf("read hold_reasons for v%d: %v", tc.version, err)
		}
		if len(got) != len(tc.want) {
			t.Fatalf("v%d hold_reasons = %v, want %v", tc.version, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("v%d hold_reasons = %v, want %v", tc.version, got, tc.want)
			}
		}
	}
}

// version_approvals decides each reason exactly once. Rejection is terminal
// and there is no un-reject; the unique constraint is what enforces it.
func TestMigration017ApprovalIsDecidedOnce(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupTestDB(t)

	var skillID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO skills (name, description) VALUES ('a:b', 'x') RETURNING id`).Scan(&skillID); err != nil {
		t.Fatalf("seed skill: %v", err)
	}
	var versionID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO skill_versions (skill_id, version, archive_path, checksum)
		VALUES ($1, 1, 'a.tar.gz', 'deadbeef') RETURNING id`, skillID).Scan(&versionID); err != nil {
		t.Fatalf("seed version: %v", err)
	}

	ins := `INSERT INTO version_approvals (version_id, reason, decision, actor_email)
	        VALUES ($1, 'ownership', $2, 'a@b.c')`
	if _, err := pool.Exec(ctx, ins, versionID, "approved"); err != nil {
		t.Fatalf("first approval: %v", err)
	}
	if _, err := pool.Exec(ctx, ins, versionID, "rejected"); err == nil {
		t.Fatal("second decision on the same reason was accepted; rejection must be terminal")
	}
}
