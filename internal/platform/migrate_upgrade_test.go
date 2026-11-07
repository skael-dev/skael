package platform_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"

	"github.com/skael-dev/skael/internal/platform"
	"github.com/skael-dev/skael/internal/testutil"
)

// TestUpgradeFromV0180 migrates a database at v0.18.0's schema, with data in
// it, to this release, and checks that nothing a user had is lost.
func TestUpgradeFromV0180(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	pool := testutil.SetupTestDBAtVersion(t, 31)
	ctx := context.Background()
	var skillID, userID string
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO skills (name, description, latest_version) VALUES ('kept', 'd', 1) RETURNING id`).Scan(&skillID))
	_, err := pool.Exec(ctx, `INSERT INTO skill_versions (skill_id, version, archive_path, checksum) VALUES ($1, 1, 'kept/abc.tar.gz', 'c')`, skillID)
	require.NoError(t, err)
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO auth_users (id, email, email_normalized, display_name, role, kind, created_at)
		VALUES (gen_random_uuid(), 'o@x.com', 'o@x.com', 'O', 'owner', 'human', now()) RETURNING id`).Scan(&userID))
	_, err = pool.Exec(ctx, `INSERT INTO skill_events (skill_name, agent, developer_hash) VALUES ('kept', 'claude-code', 'h')`)
	require.NoError(t, err)

	db := stdlib.OpenDBFromPool(pool)
	defer db.Close() //nolint:errcheck
	require.NoError(t, platform.MigrateUpTo(db, 39))

	var versions, events, owners int
	var backend string
	var sample bool
	require.NoError(t, pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM skill_versions WHERE archive_path = 'kept/abc.tar.gz'),
		(SELECT count(*) FROM skill_events WHERE user_id IS NULL AND model IS NULL),
		(SELECT count(*) FROM auth_users WHERE role = 'owner'),
		(SELECT kind FROM storage_backend),
		(SELECT sample FROM skills WHERE name = 'kept')`).Scan(&versions, &events, &owners, &backend, &sample))
	require.Equal(t, 1, versions)
	require.Equal(t, 1, events)
	require.Equal(t, 1, owners)
	require.Equal(t, "filesystem", backend, "an upgraded install must keep its archives where they are")
	require.False(t, sample)

	var claim string
	require.NoError(t, pool.QueryRow(ctx, `SELECT claim_code FROM onboarding`).Scan(&claim))
	require.Empty(t, claim, "an install with an owner never needs a claim code")
}
