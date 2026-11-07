package platform_test

import (
	"context"
	"io"
	"io/fs"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"

	"github.com/skael-dev/skael/internal/platform"
	"github.com/skael-dev/skael/internal/testutil"
)

func TestOpenStorage_ANewInstallUsesPostgresAndKeepsIt(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()

	st, at, err := platform.OpenStorage(ctx, pool, "", t.TempDir())
	require.NoError(t, err)
	require.Equal(t, "postgres", at)
	_, err = st.Write(ctx, "a/b.tar.gz", strings.NewReader("archive"))
	require.NoError(t, err)
	r, err := st.Read(ctx, "a/b.tar.gz")
	require.NoError(t, err)
	got, _ := io.ReadAll(r)
	require.Equal(t, "archive", string(got))
	_, err = st.Read(ctx, "missing")
	require.ErrorIs(t, err, fs.ErrNotExist)

	_, at, err = platform.OpenStorage(ctx, pool, "", t.TempDir())
	require.NoError(t, err)
	require.Equal(t, "postgres", at)
}

func TestMigration034KeepsAnExistingInstallOnTheFilesystem(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	pool := testutil.SetupTestDBAtVersion(t, 33)
	ctx := context.Background()
	var skillID string
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO skills (name, description) VALUES ('s', 'd') RETURNING id`).Scan(&skillID))
	_, err := pool.Exec(ctx, `INSERT INTO skill_versions (skill_id, version, archive_path, checksum) VALUES ($1, 1, 's/x.tar.gz', 'c')`, skillID)
	require.NoError(t, err)

	db := stdlib.OpenDBFromPool(pool)
	defer db.Close() //nolint:errcheck
	require.NoError(t, platform.MigrateUpTo(db, 34))

	dir := t.TempDir()
	_, at, err := platform.OpenStorage(ctx, pool, "", dir)
	require.NoError(t, err)
	require.Equal(t, dir, at)
}
