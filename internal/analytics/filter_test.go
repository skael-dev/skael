package analytics_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/skael-dev/skael/internal/analytics"
	"github.com/skael-dev/skael/internal/runs"
	"github.com/skael-dev/skael/internal/skill"
	"github.com/skael-dev/skael/internal/testutil"
)

func TestConsumerAndRoleFilters(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	store := analytics.NewStore(pool)
	insertTestSkill(t, ctx, skill.NewStore(pool), "filtered", "a skill")

	// One hook event, then automated events from two consumers and two roles.
	require.NoError(t, store.Insert(ctx, analytics.Event{SkillName: "filtered", Agent: "claude-code", DeveloperHash: "dev1"}))
	var userID string
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO auth_users (email, email_normalized, display_name) VALUES ('m@example.com', 'm@example.com', 'm') RETURNING id`).Scan(&userID))
	report := func(consumer, role string, count int) {
		events := make([]runs.Event, count)
		for i := range events {
			events[i] = runs.Event{
				Skill: "filtered", Version: 1, Agent: "codex", Role: role,
				ExternalRunID: fmt.Sprintf("%s-%s-%d", consumer, role, i), Time: time.Now(),
			}
		}
		_, err := runs.NewStore(pool).InsertBatch(ctx, runs.Consumer{ID: userID, Name: consumer}, "", events)
		require.NoError(t, err)
	}
	report("factoree", "implementer", 3)
	report("factoree", "reviewer", 2)
	report("other", "implementer", 1)

	r := chi.NewMux()
	analytics.RegisterRoutes(humachi.New(r, huma.DefaultConfig("Test API", "1.0.0")), store)
	get := func(path string, out any) {
		t.Helper()
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), out))
	}

	var summary analytics.ActivationSummary
	get("/api/skills/filtered/activations", &summary)
	require.Equal(t, 7, summary.TotalCount, "no filter counts every event")
	require.Equal(t, 1, summary.HookActivations)
	require.Equal(t, 6, summary.AutomatedActivations)
	require.Equal(t, map[string]int{"factoree": 5, "other": 1}, summary.ByConsumer)
	require.Equal(t, 1, summary.UniqueDevs, "a reporting machine is not a developer")

	cases := []struct {
		query string
		want  int
	}{
		{"consumer=factoree", 5},
		{"role=implementer", 4},
		{"consumer=factoree&role=reviewer", 2},
		{"consumer=nobody", 0},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			var summary analytics.ActivationSummary
			get("/api/skills/filtered/activations?"+tc.query, &summary)
			require.Equal(t, tc.want, summary.TotalCount)
			require.Equal(t, 0, summary.HookActivations)

			var overview analytics.OverviewData
			get("/api/analytics/overview?"+tc.query, &overview)
			require.Equal(t, tc.want, overview.TotalActivations)

			var list struct {
				Skills []analytics.SkillAnalytics `json:"skills"`
				Total  int                        `json:"total"`
			}
			get("/api/analytics/skills?"+tc.query, &list)
			if tc.want == 0 {
				require.Empty(t, list.Skills, "a filter drops skills with no match")
			} else {
				require.Len(t, list.Skills, 1)
				require.Equal(t, tc.want, list.Skills[0].Activations)
			}

			var series []analytics.DailyCount
			get("/api/analytics/timeseries?"+tc.query, &series)
			total := 0
			for _, d := range series {
				total += d.Count
			}
			require.Equal(t, tc.want, total)

			var skillSeries []map[string]any
			get("/api/skills/filtered/timeseries?"+tc.query, &skillSeries)
			total = 0
			for _, d := range skillSeries {
				if n, ok := d["codex"].(float64); ok {
					total += int(n)
				}
			}
			require.Equal(t, tc.want, total)
		})
	}

	var values analytics.AnalyticsConsumersBody
	get("/api/analytics/consumers", &values)
	require.Equal(t, []string{"factoree", "other"}, values.Consumers)
	require.Equal(t, []string{"implementer", "reviewer"}, values.Roles)
}
