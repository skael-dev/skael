package analytics_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/skael-dev/skael/internal/analytics"
	"github.com/skael-dev/skael/internal/testutil"
)

// setupAnalyticsAPI creates a Chi router + Huma API backed by a real ephemeral
// Postgres database and registers the analytics routes.
func setupAnalyticsAPI(t *testing.T) (http.Handler, *analytics.Store) {
	t.Helper()

	pool := testutil.SetupTestDB(t)
	store := analytics.NewStore(pool)

	r := chi.NewMux()
	api := humachi.New(r, huma.DefaultConfig("Test API", "1.0.0"))
	analytics.RegisterRoutes(api, store)

	return r, store
}

// doJSONAnalytics sends a JSON-body request and returns the recorder.
func doJSONAnalytics(t *testing.T, handler http.Handler, method, path string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()

	var bodyBytes []byte
	if body != nil {
		var err error
		bodyBytes, err = json.Marshal(body)
		require.NoError(t, err)
	}

	req := httptest.NewRequest(method, path, bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

// TestIngestEvent_EmptySkillName_Rejected verifies that POSTing an event with
// an empty skill_name is rejected with 422 Unprocessable Entity.
func TestIngestEvent_EmptySkillName_Rejected(t *testing.T) {
	handler, _ := setupAnalyticsAPI(t)

	rr := doJSONAnalytics(t, handler, http.MethodPost, "/api/events", map[string]string{
		"skill_name":  "",
		"agent":       "claude-sonnet",
		"trigger_type": "auto",
	})

	require.Equal(t, http.StatusUnprocessableEntity, rr.Code,
		"expected 422 for empty skill_name, got %d: %s", rr.Code, rr.Body.String())
}

// TestIngestEvent_EmptyAgent_Rejected verifies that POSTing an event with an
// empty agent field is rejected with 422 Unprocessable Entity.
func TestIngestEvent_EmptyAgent_Rejected(t *testing.T) {
	handler, _ := setupAnalyticsAPI(t)

	rr := doJSONAnalytics(t, handler, http.MethodPost, "/api/events", map[string]string{
		"skill_name":  "code-review",
		"agent":       "",
		"trigger_type": "auto",
	})

	require.Equal(t, http.StatusUnprocessableEntity, rr.Code,
		"expected 422 for empty agent, got %d: %s", rr.Code, rr.Body.String())
}

// TestIngestEvent_ValidEvent verifies that POSTing a fully-populated event is
// accepted with 204 No Content.
func TestIngestEvent_ValidEvent(t *testing.T) {
	handler, _ := setupAnalyticsAPI(t)

	rr := doJSONAnalytics(t, handler, http.MethodPost, "/api/events", map[string]string{
		"skill_name":      "code-review",
		"agent":           "claude-sonnet",
		"trigger_type":    "auto",
		"project_hash":    "proj1",
		"developer_hash":  "dev1",
	})

	require.Equal(t, http.StatusNoContent, rr.Code,
		"expected 204 for valid event, got %d: %s", rr.Code, rr.Body.String())
}

// TestGetActivations_ViaHTTP verifies that the GET /api/skills/{name}/activations
// endpoint returns 200 with a valid JSON body.
func TestGetActivations_ViaHTTP(t *testing.T) {
	handler, _ := setupAnalyticsAPI(t)

	rr := doJSONAnalytics(t, handler, http.MethodGet, "/api/skills/code-review/activations", nil)

	require.Equal(t, http.StatusOK, rr.Code,
		"expected 200, got %d: %s", rr.Code, rr.Body.String())

	var summary analytics.ActivationSummary
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &summary),
		"response body: %s", rr.Body.String())
	// No events inserted, so counts should be zero.
	require.Equal(t, 0, summary.TotalCount)
}

// TestGetSkillTimeSeries_ViaHTTP verifies that the GET /api/skills/{name}/timeseries
// endpoint returns 200 with per-agent daily data in flat JSON shape.
func TestGetSkillTimeSeries_ViaHTTP(t *testing.T) {
	handler, store := setupAnalyticsAPI(t)
	ctx := context.Background()

	require.NoError(t, store.Insert(ctx, analytics.Event{
		SkillName: "ts-skill", Agent: "claude-code", TriggerType: "auto",
		ProjectHash: "p1", DeveloperHash: "d1",
	}))

	rr := doJSONAnalytics(t, handler, http.MethodGet, "/api/skills/ts-skill/timeseries?days=7", nil)
	require.Equal(t, http.StatusOK, rr.Code, "body: %s", rr.Body.String())

	var series []map[string]interface{}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &series))
	require.NotEmpty(t, series)

	for _, entry := range series {
		_, hasDate := entry["date"]
		require.True(t, hasDate, "each entry must have a date key")
	}
}

// TestGetSkillTimeSeries_ViaHTTP_Empty verifies empty timeseries returns gap-filled days.
func TestGetSkillTimeSeries_ViaHTTP_Empty(t *testing.T) {
	handler, _ := setupAnalyticsAPI(t)

	rr := doJSONAnalytics(t, handler, http.MethodGet, "/api/skills/nonexistent/timeseries?days=7", nil)
	require.Equal(t, http.StatusOK, rr.Code, "body: %s", rr.Body.String())

	var series []map[string]interface{}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &series))
	require.Equal(t, 8, len(series))
}
