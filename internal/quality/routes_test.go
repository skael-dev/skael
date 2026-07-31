package quality_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/skael-dev/skael/internal/quality"
	"github.com/skael-dev/skael/internal/skill"
	"github.com/skael-dev/skael/internal/testutil"
)

// newQualityTestServer wires the real router with quality routes registered,
// backed by a real Postgres database.
func newQualityTestServer(t *testing.T) (http.Handler, *quality.Store, *skill.Store, *pgxpool.Pool) {
	t.Helper()
	pool := testutil.SetupTestDB(t)
	qs := quality.NewStore(pool)
	sk := skill.NewStore(pool)

	r := chi.NewMux()
	api := humachi.New(r, huma.DefaultConfig("Test API", "1.0.0"))
	quality.RegisterRoutes(api, qs, sk)
	return r, qs, sk, pool
}

func doGet(t *testing.T, handler http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func TestGetQuality_404WhenUnscored(t *testing.T) {
	handler, _, sk, _ := newQualityTestServer(t)
	_, err := sk.Create(t.Context(), "deploy-helper", "deploy-helper", "", "", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}

	rr := doGet(t, handler, "/api/skills/deploy-helper/quality")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rr.Code, rr.Body)
	}
}

func TestGetQuality_404WhenSkillDoesNotExist(t *testing.T) {
	handler, _, _, _ := newQualityTestServer(t)
	rr := doGet(t, handler, "/api/skills/does-not-exist/quality")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rr.Code, rr.Body)
	}
}

func TestGetQualityHistory_ReturnsNewestFirst(t *testing.T) {
	handler, qs, sk, _ := newQualityTestServer(t)
	created, err := sk.Create(t.Context(), "deploy-helper", "deploy-helper", "", "", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}

	base := quality.Record{
		SkillID: created.ID, Version: 1, SuiteRef: "r", Tier: "full",
		Pillars: json.RawMessage(`{}`), PanelMatrix: json.RawMessage(`[]`),
		DriftBreakdown: json.RawMessage(`{}`), ModelPanel: json.RawMessage(`[]`),
	}

	older := base
	older.Headline, older.ScoredAt = 50, time.Now().Add(-time.Hour)
	if err := qs.Upsert(t.Context(), older); err != nil {
		t.Fatal(err)
	}

	newer := base
	newer.Headline, newer.ScoredAt = 80, time.Now()
	if err := qs.Upsert(t.Context(), newer); err != nil {
		t.Fatal(err)
	}

	rr := doGet(t, handler, "/api/skills/deploy-helper/quality/history")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body)
	}

	var out struct {
		History []struct {
			Headline float64 `json:"headline_score"`
		} `json:"history"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.History) != 2 {
		t.Fatalf("history = %d rows, want 2", len(out.History))
	}
	if out.History[0].Headline != 80 || out.History[1].Headline != 50 {
		t.Fatalf("history = %+v, want newest (80) first then 50", out.History)
	}
}

func TestGetQuality_ReturnsLatestScoredVersion(t *testing.T) {
	handler, qs, sk, _ := newQualityTestServer(t)
	created, err := sk.Create(t.Context(), "deploy-helper", "deploy-helper", "", "", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	// Publishing bumps latest_version; simulate it directly so the store's
	// notion of "current version" matches what quality.Latest is queried for.
	if _, err := sk.CreateVersion(t.Context(), created.ID, "archive.tar.gz", "cksum", "",
		"", "body", json.RawMessage(`{}`), nil, json.RawMessage(`{}`), "tester"); err != nil {
		t.Fatal(err)
	}

	rec := quality.Record{
		SkillID: created.ID, Version: 1, SuiteRef: "r", Tier: "full", Headline: 90,
		Pillars: json.RawMessage(`{}`), PanelMatrix: json.RawMessage(`[]`),
		DriftBreakdown: json.RawMessage(`{}`), ModelPanel: json.RawMessage(`[]`),
		ScoredAt: time.Now(),
	}
	if err := qs.Upsert(t.Context(), rec); err != nil {
		t.Fatal(err)
	}

	rr := doGet(t, handler, "/api/skills/deploy-helper/quality")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body)
	}
	var out struct {
		Headline float64 `json:"headline_score"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Headline != 90 {
		t.Fatalf("headline = %v, want 90", out.Headline)
	}
}
