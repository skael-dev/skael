package analytics_test

import (
	"context"
	"testing"
	"time"

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

func TestStore_GetSkillTimeSeries(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	store := analytics.NewStore(pool)
	ctx := context.Background()

	events := []analytics.Event{
		{SkillName: "code-review", Agent: "claude-code", TriggerType: "auto", ProjectHash: "p1", DeveloperHash: "d1"},
		{SkillName: "code-review", Agent: "claude-code", TriggerType: "auto", ProjectHash: "p2", DeveloperHash: "d2"},
		{SkillName: "code-review", Agent: "cursor", TriggerType: "auto", ProjectHash: "p1", DeveloperHash: "d1"},
		{SkillName: "other-skill", Agent: "claude-code", TriggerType: "auto", ProjectHash: "p3", DeveloperHash: "d3"},
	}
	for _, e := range events {
		require.NoError(t, store.Insert(ctx, e))
	}

	series, err := store.GetSkillTimeSeries(ctx, "code-review", 30)
	require.NoError(t, err)
	require.NotEmpty(t, series)

	// All events inserted today — find today's row
	today := time.Now().Format("2006-01-02")
	var todayRow *analytics.AgentDailyCount
	for i := range series {
		if series[i].Date == today {
			todayRow = &series[i]
			break
		}
	}
	require.NotNil(t, todayRow, "expected a row for today")
	require.Equal(t, 2, todayRow.Agents["claude-code"])
	require.Equal(t, 1, todayRow.Agents["cursor"])

	// "other-skill" events should not appear
	for _, row := range series {
		_, hasOther := row.Agents["other-skill-agent"]
		require.False(t, hasOther)
	}

	// Gap-fill: every day in the 30-day window should be present
	require.Equal(t, 31, len(series)) // 30 days + today
}

func TestStore_GetSkillTimeSeries_NoEvents(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	store := analytics.NewStore(pool)
	ctx := context.Background()

	series, err := store.GetSkillTimeSeries(ctx, "nonexistent", 7)
	require.NoError(t, err)
	require.NotNil(t, series)
	require.Equal(t, 8, len(series)) // 7 days + today, all with empty agents
	for _, row := range series {
		require.Empty(t, row.Agents)
	}
}
