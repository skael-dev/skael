package platform_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/skael-dev/skael/internal/platform"
	"github.com/skael-dev/skael/internal/testutil"
)

// A candidate stored before sets keeps its version, and after the migration a
// candidate is exactly one of a version, a PR head or a set resolution.
func TestMigration044AddsSetCandidates(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupTestDBAtVersion(t, 42)

	var skillID, contestID, setID string
	if err := pool.QueryRow(ctx, `INSERT INTO skills (name) VALUES ('legacy-044') RETURNING id`).Scan(&skillID); err != nil {
		t.Fatalf("seed skill: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO contests (suite_ref, suite_provenance) VALUES ('ref-044', 'authored') RETURNING id`).Scan(&contestID); err != nil {
		t.Fatalf("seed contest: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO contest_candidates (contest_id, position, skill_id, skill_name, version, label)
		VALUES ($1, 0, $2, 'legacy-044', 1, 'legacy-044@1')`, contestID, skillID); err != nil {
		t.Fatalf("seed candidate: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO sets (name) VALUES ('team-044') RETURNING id`).Scan(&setID); err != nil {
		t.Fatalf("seed set: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO set_resolutions (id, set_id, set_name, entries, complete)
		VALUES ('res-044', $1, 'team-044', '[]', true)`, setID); err != nil {
		t.Fatalf("seed resolution: %v", err)
	}

	db := stdlib.OpenDBFromPool(pool)
	defer db.Close() //nolint:errcheck
	if err := platform.MigrateUpTo(db, 44); err != nil {
		t.Fatalf("migrate to 44: %v", err)
	}

	var version int
	if err := pool.QueryRow(ctx, `SELECT version FROM contest_candidates WHERE contest_id = $1`, contestID).Scan(&version); err != nil || version != 1 {
		t.Fatalf("existing candidate: version %d, err %v", version, err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO contest_candidates (contest_id, position, skill_name, label, set_id, set_name, resolution_id)
		VALUES ($1, 1, '', 'set team-044@res-044', $2, 'team-044', 'res-044')`, contestID, setID); err != nil {
		t.Fatalf("a set candidate with no skill was refused: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO contest_candidates (contest_id, position, skill_id, skill_name, version, label, set_id, resolution_id)
		VALUES ($1, 2, $2, 'legacy-044', 2, 'both', $3, 'res-044')`, contestID, skillID, setID); err == nil {
		t.Error("a candidate that is both a version and a set was stored")
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO contest_candidates (contest_id, position, skill_name, label, set_id, set_name, resolution_id)
		VALUES ($1, 3, '', 'set team-044@missing', $2, 'team-044', 'missing')`, contestID, setID); err == nil {
		t.Error("a candidate naming a resolution that was never stored was accepted")
	}
}
