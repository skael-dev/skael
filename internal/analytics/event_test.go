package analytics_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skael-dev/skael/internal/analytics"
	"github.com/skael-dev/skael/internal/testutil"
)

func TestStore_InsertAndQuery(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	store := analytics.NewStore(pool)
	ctx := context.Background()

	// Insert 3 events for "code-review" across 2 agents and 2 developers.
	events := []analytics.Event{
		{SkillName: "code-review", Agent: "claude-sonnet", TriggerType: "auto", ProjectHash: "proj1", DeveloperHash: "dev1"},
		{SkillName: "code-review", Agent: "claude-sonnet", TriggerType: "auto", ProjectHash: "proj2", DeveloperHash: "dev2"},
		{SkillName: "code-review", Agent: "claude-opus", TriggerType: "manual", ProjectHash: "proj1", DeveloperHash: "dev1"},
		// Unrelated event for a different skill.
		{SkillName: "deployment", Agent: "claude-sonnet", TriggerType: "auto", ProjectHash: "proj3", DeveloperHash: "dev3"},
	}
	for _, e := range events {
		require.NoError(t, store.Insert(ctx, e))
	}

	summary, err := store.GetActivations(ctx, "code-review", 30)
	require.NoError(t, err)
	require.NotNil(t, summary)

	require.Equal(t, 3, summary.TotalCount)
	require.Equal(t, 2, summary.UniqueDevs)
	require.NotNil(t, summary.LastTriggered)
	require.Len(t, summary.ByAgent, 2)
	require.Equal(t, 2, summary.ByAgent["claude-sonnet"])
	require.Equal(t, 1, summary.ByAgent["claude-opus"])
}

func TestStore_GetActivations_NoEvents(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	store := analytics.NewStore(pool)
	ctx := context.Background()

	summary, err := store.GetActivations(ctx, "nonexistent", 30)
	require.NoError(t, err)
	require.NotNil(t, summary)
	require.Equal(t, 0, summary.TotalCount)
	require.Equal(t, 0, summary.UniqueDevs)
	require.Nil(t, summary.LastTriggered)
	require.Empty(t, summary.ByAgent)
}
