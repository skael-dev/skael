package analytics_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skael-dev/skael/internal/analytics"
	"github.com/skael-dev/skael/internal/testutil"
)

func TestBreakdown_AMemberSeesOnlyGroupsOfFiveAndTheirOwnCount(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	store := analytics.NewStore(pool)

	var users []string
	for i := 0; i < 6; i++ {
		var id string
		require.NoError(t, pool.QueryRow(ctx, `INSERT INTO auth_users (id, email, email_normalized, display_name, role, kind, created_at)
			VALUES (gen_random_uuid(), $1, $1, $2, 'member', 'human', now()) RETURNING id`,
			fmt.Sprintf("u%d@x.com", i), fmt.Sprintf("User %d", i)).Scan(&id))
		users = append(users, id)
	}
	for _, u := range users {
		require.NoError(t, store.Insert(ctx, analytics.Event{SkillName: "s", Agent: "claude-code", UserID: u, Model: "claude-sonnet-5", HarnessVersion: "2.1.0"}))
	}
	require.NoError(t, store.Insert(ctx, analytics.Event{SkillName: "s", Agent: "codex", UserID: users[0], Model: "gpt-5"}))

	member, err := store.GetBreakdown(ctx, 30, "", users[0], false, analytics.VisibilityAggregate)
	require.NoError(t, err)
	require.Len(t, member.ByModel, 1)
	require.Equal(t, "claude-sonnet-5", member.ByModel[0].Key)
	require.Equal(t, 2, member.Hidden)
	require.Nil(t, member.ByPerson)
	require.Equal(t, 2, member.Mine)

	admin, err := store.GetBreakdown(ctx, 30, "", users[1], true, analytics.VisibilityAggregate)
	require.NoError(t, err)
	require.Len(t, admin.ByModel, 2)
	require.Len(t, admin.ByPerson, 6)
	require.True(t, admin.IdentityRecorded)
}
