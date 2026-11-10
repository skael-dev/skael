package platform_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"

	"github.com/skael-dev/skael/internal/platform"
	"github.com/skael-dev/skael/internal/testutil"
)

// Every set entry written before patterns existed is an exact skill name.
func TestMigration041MarksExistingSetEntriesAsNames(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	pool := testutil.SetupTestDBAtVersion(t, 40)
	ctx := context.Background()
	var setID string
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO sets (name, description, created_by) VALUES ('team', '', 'o') RETURNING id`).Scan(&setID))
	_, err := pool.Exec(ctx, `INSERT INTO set_entries (set_id, skill_name, version) VALUES ($1, 'alpha', 0), ($1, 'beta', 3)`, setID)
	require.NoError(t, err)

	db := stdlib.OpenDBFromPool(pool)
	defer db.Close() //nolint:errcheck
	require.NoError(t, platform.MigrateUpTo(db, 41))

	var names int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM set_entries WHERE kind = 'name'`).Scan(&names))
	require.Equal(t, 2, names)
	_, err = pool.Exec(ctx, `INSERT INTO set_entries (set_id, skill_name, kind) VALUES ($1, 'x', 'glob')`, setID)
	require.Error(t, err, "an unknown kind must be refused")
}
