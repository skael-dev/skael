package evalqueue_test

import (
	"context"
	"testing"

	"github.com/skael-dev/skael/internal/testutil"
)

func TestMigration_CreatesEvalTables(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()

	for _, table := range []string{"eval_jobs", "eval_suites", "skill_quality"} {
		var n int
		err := pool.QueryRow(ctx,
			`SELECT count(*) FROM information_schema.tables WHERE table_name = $1`, table).Scan(&n)
		if err != nil {
			t.Fatalf("%s: %v", table, err)
		}
		if n != 1 {
			t.Fatalf("table %s: got %d, want 1", table, n)
		}
	}
}

// A composite unique constraint including the natural key would have to be
// rebuilt when org_id lands. Assert the primary keys are single UUID columns.
func TestMigration_PrimaryKeysAreSingleColumn(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()

	for _, table := range []string{"eval_jobs", "eval_suites", "skill_quality"} {
		rows, err := pool.Query(ctx, `
			SELECT a.attname
			FROM pg_index i
			JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY(i.indkey)
			WHERE i.indrelid = $1::regclass AND i.indisprimary`, table)
		if err != nil {
			t.Fatalf("%s: %v", table, err)
		}
		var cols []string
		for rows.Next() {
			var c string
			if err := rows.Scan(&c); err != nil {
				t.Fatal(err)
			}
			cols = append(cols, c)
		}
		rows.Close()
		if len(cols) != 1 || cols[0] != "id" {
			t.Fatalf("%s primary key = %v, want [id]", table, cols)
		}
	}
}
