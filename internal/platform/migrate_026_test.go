package platform_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"

	"github.com/skael-dev/skael/internal/platform"
	"github.com/skael-dev/skael/internal/testutil"
)

func TestMigration026MovesPeopleToAuthAll(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	pool := testutil.SetupTestDBAtVersion(t, 25)
	ctx := context.Background()

	var owner, service, key string
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO users (email, name, password_hash, role) VALUES ('Owner@X.com', 'O', '$2a$x', 'owner') RETURNING id`).Scan(&owner))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO users (email, name, password_hash, role, kind) VALUES ('svc@service-account.invalid', 'svc', '', 'member', 'service') RETURNING id`).Scan(&service))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO user_api_keys (user_id, name, key_prefix, key_hash) VALUES ($1, 'k', 'sk-abc', 'h') RETURNING id`, service).Scan(&key))

	db := stdlib.OpenDBFromPool(pool)
	defer db.Close() //nolint:errcheck
	require.NoError(t, platform.MigrateUpTo(db, 26))

	var normalized, name, role, kind string
	require.NoError(t, pool.QueryRow(ctx, `SELECT email_normalized, display_name, role, kind FROM auth_users WHERE id = $1`, owner).Scan(&normalized, &name, &role, &kind))
	require.Equal(t, "owner@x.com", normalized)
	require.Equal(t, "O", name)
	require.Equal(t, "owner", role)
	require.Equal(t, "human", kind)

	require.NoError(t, pool.QueryRow(ctx, `SELECT kind FROM auth_users WHERE id = $1`, service).Scan(&kind))
	require.Equal(t, "service", kind)

	var keyOwner string
	require.NoError(t, pool.QueryRow(ctx, `SELECT user_id FROM user_api_keys WHERE id = $1`, key).Scan(&keyOwner))
	require.Equal(t, service, keyOwner)

	var credentials int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM auth_credentials`).Scan(&credentials))
	require.Zero(t, credentials)

	_, err := pool.Exec(ctx, `DELETE FROM auth_users WHERE id = $1`, service)
	require.NoError(t, err)
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM user_api_keys WHERE id = $1`, key).Scan(&credentials))
	require.Zero(t, credentials, "the key's foreign key must follow the rename")
}
