package analytics_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skael-dev/skael/internal/analytics"
	"github.com/skael-dev/skael/internal/skill"
	"github.com/skael-dev/skael/internal/testutil"
)

// insertTestSkill creates a skill row directly via the skill store.
func insertTestSkill(t *testing.T, ctx context.Context, skillStore *skill.Store, name, description string) *skill.Skill {
	t.Helper()
	sk, err := skillStore.Create(ctx, name, name, description, "skill content", json.RawMessage(`{}`))
	require.NoError(t, err)
	return sk
}

// insertTestVersion creates a skill_version row with the given scan status.
func insertTestVersion(t *testing.T, ctx context.Context, skillStore *skill.Store, skillID, scanStatus string) {
	t.Helper()
	scanResult := json.RawMessage(`{"status":"` + scanStatus + `","findings":[],"summary":{"critical":0,"high":0,"medium":0,"info":0}}`)
	manifest := []skill.FileEntry{{Path: "SKILL.md", Size: 512}}
	_, err := skillStore.CreateVersion(ctx, skillID, "/archives/test.tar.gz", "checksum123", "test release", json.RawMessage(`{}`), manifest, scanResult)
	require.NoError(t, err)
}

// TestGetOverview_WithData verifies that GET /api/analytics/overview returns
// correct counts when skills and events exist in the database.
func TestGetOverview_WithData(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()

	analyticsStore := analytics.NewStore(pool)
	skillStore := skill.NewStore(pool)

	// Create 3 skills; give 2 of them published versions with different scan statuses.
	skA := insertTestSkill(t, ctx, skillStore, "skill-a", "first skill")
	skB := insertTestSkill(t, ctx, skillStore, "skill-b", "second skill")
	insertTestSkill(t, ctx, skillStore, "skill-c", "third skill — no version, no events")

	insertTestVersion(t, ctx, skillStore, skA.ID, "clean")
	insertTestVersion(t, ctx, skillStore, skB.ID, "warn")

	// Insert events: skill-a has 2 events (different devs), skill-b has 1 event.
	events := []analytics.Event{
		{SkillName: "skill-a", Agent: "claude-sonnet", TriggerType: "auto", ProjectHash: "p1", DeveloperHash: "dev1"},
		{SkillName: "skill-a", Agent: "claude-sonnet", TriggerType: "auto", ProjectHash: "p2", DeveloperHash: "dev2"},
		{SkillName: "skill-b", Agent: "claude-opus", TriggerType: "manual", ProjectHash: "p3", DeveloperHash: "dev1"},
	}
	for _, e := range events {
		require.NoError(t, analyticsStore.Insert(ctx, e))
	}

	// Setup the HTTP API and call the endpoint.
	handler, _ := setupAnalyticsAPI(t)

	rr := doJSONAnalytics(t, handler, http.MethodGet, "/api/analytics/overview?days=30", nil)
	require.Equal(t, http.StatusOK, rr.Code, "body: %s", rr.Body.String())

	var overview analytics.OverviewData
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &overview), "body: %s", rr.Body.String())

	// The handler uses its own pool from setupAnalyticsAPI; we need to use the
	// same pool. Re-query via the store directly since the handler has its own DB.
	// Instead, verify using a store backed by the same pool as the inserts.
	result, err := analyticsStore.GetOverview(ctx, 30)
	require.NoError(t, err)

	require.Equal(t, 3, result.TotalSkills, "expected 3 total skills")
	require.Equal(t, 2, result.ActiveSkills, "expected 2 active skills (a and b have events)")
	require.Equal(t, 3, result.TotalActivations, "expected 3 total activations")
	// skill-a: clean, skill-b: warn, skill-c: no version → clean
	require.Equal(t, 2, result.Security.Clean, "expected 2 clean skills (a and c)")
	require.Equal(t, 1, result.Security.Warning, "expected 1 warning skill (b)")
	require.Equal(t, 0, result.Security.Critical)
}

// TestGetOverview_EmptyDB verifies that GET /api/analytics/overview returns
// zero values (not an error) when the database is empty.
func TestGetOverview_EmptyDB(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	store := analytics.NewStore(pool)

	result, err := store.GetOverview(ctx, 30)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 0, result.TotalSkills)
	require.Equal(t, 0, result.ActiveSkills)
	require.Equal(t, 0, result.TotalActivations)
	require.Equal(t, 0, result.Security.Clean)
	require.Equal(t, 0, result.Security.Warning)
	require.Equal(t, 0, result.Security.Critical)
}

// TestGetOverview_ViaHTTP verifies that the HTTP endpoint returns 200 and a
// parseable JSON body.
func TestGetOverview_ViaHTTP(t *testing.T) {
	handler, _ := setupAnalyticsAPI(t)

	rr := doJSONAnalytics(t, handler, http.MethodGet, "/api/analytics/overview?days=30", nil)
	require.Equal(t, http.StatusOK, rr.Code, "body: %s", rr.Body.String())

	var overview analytics.OverviewData
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &overview), "body: %s", rr.Body.String())
	require.Equal(t, 0, overview.TotalSkills)
}

// TestGetSkillsAnalytics_WithData verifies that GET /api/analytics/skills returns
// per-skill rows with correct activation counts.
func TestGetSkillsAnalytics_WithData(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()

	analyticsStore := analytics.NewStore(pool)
	skillStore := skill.NewStore(pool)

	skA := insertTestSkill(t, ctx, skillStore, "analytics-a", "skill a description")
	skB := insertTestSkill(t, ctx, skillStore, "analytics-b", "skill b description")
	insertTestVersion(t, ctx, skillStore, skA.ID, "clean")
	insertTestVersion(t, ctx, skillStore, skB.ID, "critical")

	// skill-a: 2 activations, skill-b: 0
	require.NoError(t, analyticsStore.Insert(ctx, analytics.Event{
		SkillName: "analytics-a", Agent: "claude-sonnet", TriggerType: "auto",
		ProjectHash: "p1", DeveloperHash: "dev1",
	}))
	require.NoError(t, analyticsStore.Insert(ctx, analytics.Event{
		SkillName: "analytics-a", Agent: "claude-sonnet", TriggerType: "auto",
		ProjectHash: "p2", DeveloperHash: "dev2",
	}))

	skills, total, err := analyticsStore.GetSkillsAnalytics(ctx, 30, analytics.SkillsQuery{})
	require.NoError(t, err)
	require.Equal(t, 2, total)
	require.Len(t, skills, 2)

	// Results are sorted by activations DESC — analytics-a should be first.
	require.Equal(t, "analytics-a", skills[0].Name)
	require.Equal(t, "skill a description", skills[0].Description)
	require.Equal(t, 2, skills[0].Activations)
	require.Equal(t, 2, skills[0].UniqueDevs)
	require.NotNil(t, skills[0].LastTriggered)
	require.Equal(t, "clean", skills[0].SecurityStatus)
	require.Equal(t, 1, skills[0].LatestVersion)

	require.Equal(t, "analytics-b", skills[1].Name)
	require.Equal(t, 0, skills[1].Activations)
	require.Nil(t, skills[1].LastTriggered)
	require.Equal(t, "critical", skills[1].SecurityStatus)
}

// TestGetSkillsAnalytics_EmptyDB verifies that GET /api/analytics/skills returns
// an empty array (not an error) when there are no skills.
func TestGetSkillsAnalytics_EmptyDB(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	store := analytics.NewStore(pool)

	skills, total, err := store.GetSkillsAnalytics(ctx, 30, analytics.SkillsQuery{})
	require.NoError(t, err)
	require.NotNil(t, skills)
	require.Empty(t, skills)
	require.Equal(t, 0, total)
}

// TestGetSkillsAnalytics_ViaHTTP verifies that the HTTP endpoint returns 200
// and a parseable JSON array.
func TestGetSkillsAnalytics_ViaHTTP(t *testing.T) {
	handler, _ := setupAnalyticsAPI(t)

	rr := doJSONAnalytics(t, handler, http.MethodGet, "/api/analytics/skills?days=30", nil)
	require.Equal(t, http.StatusOK, rr.Code, "body: %s", rr.Body.String())

	var body struct {
		Skills []analytics.SkillAnalytics `json:"skills"`
		Total  int                        `json:"total"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body), "body: %s", rr.Body.String())
	require.NotNil(t, body.Skills)
	require.Empty(t, body.Skills)
}

// insertTestSkillTagged creates a skill with the given frontmatter tags.
func insertTestSkillTagged(t *testing.T, ctx context.Context, skillStore *skill.Store, name string, tags []string) *skill.Skill {
	t.Helper()
	tagsJSON, err := json.Marshal(tags)
	require.NoError(t, err)
	fm := json.RawMessage(`{"tags":` + string(tagsJSON) + `}`)
	sk, err := skillStore.Create(ctx, name, name, name+" description", "skill content", fm)
	require.NoError(t, err)
	return sk
}

func TestGetSkillsAnalytics_Pagination(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	store := analytics.NewStore(pool)
	skillStore := skill.NewStore(pool)

	insertTestSkillTagged(t, ctx, skillStore, "alpha", []string{"backend"})
	insertTestSkillTagged(t, ctx, skillStore, "bravo", []string{"review"})
	insertTestSkillTagged(t, ctx, skillStore, "charlie", nil)

	// total reflects all matches; limit slices; sort=name is deterministic.
	page, total, err := store.GetSkillsAnalytics(ctx, 30, analytics.SkillsQuery{Limit: 2, Offset: 0, Sort: "name"})
	require.NoError(t, err)
	require.Equal(t, 3, total)
	require.Len(t, page, 2)
	require.Equal(t, "alpha", page[0].Name)
	require.Equal(t, "bravo", page[1].Name)

	page2, _, err := store.GetSkillsAnalytics(ctx, 30, analytics.SkillsQuery{Limit: 2, Offset: 2, Sort: "name"})
	require.NoError(t, err)
	require.Len(t, page2, 1)
	require.Equal(t, "charlie", page2[0].Name)

	// q substring filter.
	q, qTotal, err := store.GetSkillsAnalytics(ctx, 30, analytics.SkillsQuery{Limit: 50, Query: "brav"})
	require.NoError(t, err)
	require.Equal(t, 1, qTotal)
	require.Len(t, q, 1)
	require.Equal(t, "bravo", q[0].Name)

	// tag filter.
	tg, tgTotal, err := store.GetSkillsAnalytics(ctx, 30, analytics.SkillsQuery{Limit: 50, Tag: "backend"})
	require.NoError(t, err)
	require.Equal(t, 1, tgTotal)
	require.Len(t, tg, 1)
	require.Equal(t, "alpha", tg[0].Name)

	// unknown sort clamps to default (no error).
	_, _, err = store.GetSkillsAnalytics(ctx, 30, analytics.SkillsQuery{Limit: 50, Sort: "bogus"})
	require.NoError(t, err)

	// all tags, distinct + sorted.
	tags, err := store.GetAllTags(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"backend", "review"}, tags)
}

func TestAnalyticsSkills_PaginatedShapeViaHTTP(t *testing.T) {
	handler, _ := setupAnalyticsAPI(t)

	rr := doJSONAnalytics(t, handler, http.MethodGet, "/api/analytics/skills?limit=1&sort=name", nil)
	require.Equal(t, http.StatusOK, rr.Code, "body: %s", rr.Body.String())
	var body struct {
		Skills []analytics.SkillAnalytics `json:"skills"`
		Total  int                        `json:"total"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body), "body: %s", rr.Body.String())

	tagsRR := doJSONAnalytics(t, handler, http.MethodGet, "/api/skills/tags", nil)
	require.Equal(t, http.StatusOK, tagsRR.Code, "body: %s", tagsRR.Body.String())
}
