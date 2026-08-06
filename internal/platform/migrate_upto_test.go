package platform_test

import (
	"context"
	"testing"

	"github.com/skael-dev/skael/internal/testutil"
)

// A database built at version 16 must not have the columns migration 17 adds.
// If SetupTestDBAtVersion silently applied everything, this would fail — which
// is the point: it is the guard on the guard.
func TestSetupTestDBAtVersionStopsShort(t *testing.T) {
	pool := testutil.SetupTestDBAtVersion(t, 16)
	ctx := context.Background()

	var exists bool
	err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_name = 'skill_versions' AND column_name = 'hold_reasons'
		)`).Scan(&exists)
	if err != nil {
		t.Fatalf("query columns: %v", err)
	}
	if exists {
		t.Fatal("database built at version 16 already has hold_reasons; " +
			"SetupTestDBAtVersion is applying every migration and proves nothing")
	}

	var version int64
	if err := pool.QueryRow(ctx,
		`SELECT max(version_id) FROM goose_db_version`).Scan(&version); err != nil {
		t.Fatalf("read goose version: %v", err)
	}
	if version != 16 {
		t.Fatalf("goose version = %d, want 16", version)
	}
}
