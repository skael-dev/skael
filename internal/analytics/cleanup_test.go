package analytics_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skael-dev/skael/internal/analytics"
	"github.com/skael-dev/skael/internal/testutil"
)

// TestStore_CleanupOldEvents_DeletesOnlyExpiredRows covers the retention purge
// that runs on server startup.
//
// The query used to interpolate the day count into text (`($1 || ' days')`),
// which made Postgres infer a text parameter that pgx could not encode an int
// into — so every startup logged "event retention cleanup failed" and nothing
// was ever purged. Nothing exercised this function, so it went unnoticed.
func TestStore_CleanupOldEvents_DeletesOnlyExpiredRows(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	pool := testutil.SetupTestDB(t)
	store := analytics.NewStore(pool)
	ctx := context.Background()

	_, err := pool.Exec(ctx,
		`INSERT INTO skill_events (skill_name, agent, created_at)
		 VALUES ('old-one', 'claude-code', now() - interval '120 days'),
		        ('old-two', 'claude-code', now() - interval '91 days'),
		        ('recent',  'claude-code', now() - interval '5 days')`)
	require.NoError(t, err)

	removed, err := store.CleanupOldEvents(ctx, 90)
	require.NoError(t, err, "retention cleanup must not error")
	assert.Equal(t, int64(2), removed, "both events older than the window must be purged")

	var remaining []string
	rows, err := pool.Query(ctx, `SELECT skill_name FROM skill_events ORDER BY skill_name`)
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		remaining = append(remaining, name)
	}
	require.NoError(t, rows.Err())

	assert.Equal(t, []string{"recent"}, remaining, "events inside the window must survive")
}

// TestStore_CleanupOldEvents_ZeroRowsIsNotAnError confirms a purge that matches
// nothing succeeds quietly, since it runs on every startup.
func TestStore_CleanupOldEvents_ZeroRowsIsNotAnError(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	pool := testutil.SetupTestDB(t)
	store := analytics.NewStore(pool)
	ctx := context.Background()

	removed, err := store.CleanupOldEvents(ctx, 90)
	require.NoError(t, err)
	assert.Equal(t, int64(0), removed)
}
