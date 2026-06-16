package server

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/platform"
	"github.com/skael-dev/skael/internal/testutil"
	"github.com/stretchr/testify/require"
)

// TestReadinessChecks_OK verifies that readinessChecks returns ("ok","ok",true)
// when both the database and local storage are healthy.
func TestReadinessChecks_OK(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	pool := testutil.SetupTestDB(t)
	dir := t.TempDir()
	storage, err := platform.NewLocalStorage(dir)
	require.NoError(t, err)

	checks, ready := readinessChecks(context.Background(), pool, storage)
	require.True(t, ready)
	require.Equal(t, "ok", checks.Database)
	require.Equal(t, "ok", checks.Storage)
}

// TestReadinessChecks_StorageFailure verifies that when storage is unreachable:
//   - ready is false
//   - checks.Storage is "unavailable" (not the real error message / temp path)
//   - the temp dir path does not leak into either check field
func TestReadinessChecks_StorageFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	pool := testutil.SetupTestDB(t)
	dir := t.TempDir()
	storage, err := platform.NewLocalStorage(dir)
	require.NoError(t, err)

	// Remove the storage directory so Ping fails.
	require.NoError(t, os.RemoveAll(dir))

	checks, ready := readinessChecks(context.Background(), pool, storage)
	require.False(t, ready)
	require.Equal(t, "ok", checks.Database, "database should still be ok")
	require.Equal(t, "unavailable", checks.Storage, "storage status must be 'unavailable', not the raw error")

	// The temp dir path must not leak into any check field.
	require.False(t, strings.Contains(checks.Storage, dir), "storage field must not contain the temp path")
	require.False(t, strings.Contains(checks.Database, dir), "database field must not contain the temp path")
}
