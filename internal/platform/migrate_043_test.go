package platform_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"

	"github.com/skael-dev/skael/internal/platform"
	"github.com/skael-dev/skael/internal/testutil"
)

// A version released before 043 keeps its state and its report, has no release
// report until its first rescan, and can become withdrawn.
func TestMigration043AddsWithdrawnToExistingVersions(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	pool := testutil.SetupTestDBAtVersion(t, 41)
	ctx := context.Background()
	var skillID string
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO skills (name) VALUES ('alpha') RETURNING id`).Scan(&skillID))
	_, err := pool.Exec(ctx, `
		INSERT INTO skill_versions (skill_id, version, archive_path, checksum, scan_result)
		VALUES ($1, 1, 'a', 'sum', '{"status":"clean"}')`, skillID)
	require.NoError(t, err)

	db := stdlib.OpenDBFromPool(pool)
	defer db.Close() //nolint:errcheck
	require.NoError(t, platform.MigrateUpTo(db, 43))

	var state, status string
	var baseline *string
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT gate_state, scan_result->>'status', release_scan_result::text FROM skill_versions`).Scan(&state, &status, &baseline))
	require.Equal(t, "released", state)
	require.Equal(t, "clean", status)
	require.Nil(t, baseline)

	_, err = pool.Exec(ctx, `UPDATE skill_versions SET gate_state = 'withdrawn'`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE skill_versions SET gate_state = 'gone'`)
	require.Error(t, err, "an unknown state must be refused")
}
