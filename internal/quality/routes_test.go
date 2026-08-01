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

	"github.com/skael-dev/skael/internal/auth"
	"github.com/skael-dev/skael/internal/gate"
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
		SkillID: created.ID, SuiteRef: "r", Tier: "full",
		Pillars: json.RawMessage(`{}`), PanelMatrix: json.RawMessage(`[]`),
		DriftBreakdown: json.RawMessage(`{}`), ModelPanel: json.RawMessage(`[]`),
	}

	// Two distinct versions, not a re-score of the same one — history exists
	// for the version-over-version trend, so a test that scores the same
	// version twice only proves ordering, not the trend the endpoint serves.
	older := base
	older.Version, older.Headline, older.ScoredAt = 1, 50, time.Now().Add(-time.Hour)
	if err := qs.Upsert(t.Context(), older); err != nil {
		t.Fatal(err)
	}

	newer := base
	newer.Version, newer.Headline, newer.ScoredAt = 2, 80, time.Now()
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
	// notion of "current version" matches what the record scores.
	if _, err := sk.CreateVersion(t.Context(), created.ID, "archive.tar.gz", "cksum", "",
		"", "body", json.RawMessage(`{}`), nil, json.RawMessage(`{}`), "tester", allowDecision()); err != nil {
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

// A skill scored at an earlier version must keep reporting that score after
// a later, not-yet-scored version becomes current — otherwise the quality
// badge disappears on every publish until the eval worker catches up, and
// never returns at all during a queue outage.
func TestGetQuality_KeepsShowingAnEarlierVersionsScoreAfterANewerUnscoredPublish(t *testing.T) {
	handler, qs, sk, _ := newQualityTestServer(t)
	created, err := sk.Create(t.Context(), "deploy-helper", "deploy-helper", "", "", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}

	// v1: published and scored.
	if _, err := sk.CreateVersion(t.Context(), created.ID, "archive-v1.tar.gz", "cksum1", "",
		"", "body", json.RawMessage(`{}`), nil, json.RawMessage(`{}`), "tester", allowDecision()); err != nil {
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

	// v2: published, but no eval has landed for it yet — the skill's
	// latest_version is now 2, and skill_quality has no row for it.
	if _, err := sk.CreateVersion(t.Context(), created.ID, "archive-v2.tar.gz", "cksum2", "",
		"", "body", json.RawMessage(`{}`), nil, json.RawMessage(`{}`), "tester", allowDecision()); err != nil {
		t.Fatal(err)
	}

	rr := doGet(t, handler, "/api/skills/deploy-helper/quality")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (v1's score should still show): %s", rr.Code, rr.Body)
	}
	var out struct {
		Version  int     `json:"version"`
		Headline float64 `json:"headline_score"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Version != 1 || out.Headline != 90 {
		t.Fatalf("quality = %+v, want version 1 headline 90", out)
	}
}

// /quality/series must not be captured by the {version} route.
func TestGetQualitySeries_NotShadowedByVersionRoute(t *testing.T) {
	handler, qs, sk, _ := newQualityTestServer(t)
	created, err := sk.Create(t.Context(), "series-shadow", "series-shadow", "", "", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	seedRecordWithReport(t, qs, created.ID, 1, nil)

	rr := doGet(t, handler, "/api/skills/series-shadow/quality/series")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body)
	}
	var body struct {
		Series []struct {
			Current bool `json:"current"`
		} `json:"series"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("series route returned a non-series body: %v", err)
	}
	if len(body.Series) != 1 || !body.Series[0].Current {
		t.Fatalf("series = %+v, want one current series", body.Series)
	}
}

// allowDecision is the clean-scan gate decision: nothing to hold on.
func allowDecision() gate.Decision {
	return gate.Decision{Outcome: gate.Allow, Reasons: []gate.Reason{}}
}

// needsReviewDecision holds the version for review, matching what a blocking
// (but appealable) scan finding produces on publish.
func needsReviewDecision() gate.Decision {
	return gate.Decision{Outcome: gate.NeedsReview, Reasons: []gate.Reason{}}
}

// doGetAs mirrors doGet but attaches an auth user to the request context,
// the same approach internal/evalqueue/routes_test.go uses to exercise
// privilege checks at the HTTP layer.
func doGetAs(t *testing.T, handler http.Handler, path string, user *auth.User) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req = req.WithContext(auth.ContextWithUser(req.Context(), user))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func TestGetQualityVersion_HeldVersion_PrivilegedSeesReport(t *testing.T) {
	handler, qs, sk, _ := newQualityTestServer(t)
	created, err := sk.Create(t.Context(), "held-skill", "held-skill", "", "", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sk.CreateVersion(t.Context(), created.ID, "archive.tar.gz", "cksum", "",
		"desc", "body", json.RawMessage(`{}`), nil, json.RawMessage(`{}`), "tester",
		needsReviewDecision()); err != nil {
		t.Fatal(err)
	}
	seedRecordWithReport(t, qs, created.ID, 1,
		json.RawMessage(`{"schema_version":1,"headline":74.2,"judge_note":{"evidence":"quoted skill text"}}`))

	admin := &auth.User{ID: "1", Email: "admin@example.com", Role: auth.RoleAdmin}
	rr := doGetAs(t, handler, "/api/skills/held-skill/quality/1", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body)
	}
	var body struct {
		Report json.RawMessage `json:"report"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Report) == 0 || string(body.Report) == "null" {
		t.Fatalf("report = %q, want the stored report for a privileged caller", body.Report)
	}
}

func TestGetQualityVersion_HeldVersion_NonPrivilegedGetsNullReport(t *testing.T) {
	handler, qs, sk, _ := newQualityTestServer(t)
	created, err := sk.Create(t.Context(), "held-skill-2", "held-skill-2", "", "", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sk.CreateVersion(t.Context(), created.ID, "archive.tar.gz", "cksum", "",
		"desc", "body", json.RawMessage(`{}`), nil, json.RawMessage(`{}`), "tester",
		needsReviewDecision()); err != nil {
		t.Fatal(err)
	}
	seedRecordWithReport(t, qs, created.ID, 1,
		json.RawMessage(`{"schema_version":1,"headline":74.2,"judge_note":{"evidence":"quoted skill text"}}`))

	member := &auth.User{ID: "2", Email: "member@example.com", Role: auth.RoleMember}
	rr := doGetAs(t, handler, "/api/skills/held-skill-2/quality/1", member)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body)
	}
	var body struct {
		Headline float64         `json:"headline_score"`
		Report   json.RawMessage `json:"report"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Headline != 74.2 {
		t.Fatalf("headline = %v, want 74.2 (aggregates must still be served)", body.Headline)
	}
	if string(body.Report) != "null" {
		t.Fatalf("report = %q, want null for a non-privileged caller on a held version", body.Report)
	}
}

func TestGetQualityVersion_ReleasedVersion_NonPrivilegedSeesReport(t *testing.T) {
	handler, qs, sk, _ := newQualityTestServer(t)
	created, err := sk.Create(t.Context(), "released-skill", "released-skill", "", "", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sk.CreateVersion(t.Context(), created.ID, "archive.tar.gz", "cksum", "",
		"desc", "body", json.RawMessage(`{}`), nil, json.RawMessage(`{}`), "tester",
		allowDecision()); err != nil {
		t.Fatal(err)
	}
	seedRecordWithReport(t, qs, created.ID, 1,
		json.RawMessage(`{"schema_version":1,"headline":74.2,"judge_note":{"evidence":"quoted skill text"}}`))

	member := &auth.User{ID: "3", Email: "member2@example.com", Role: auth.RoleMember}
	rr := doGetAs(t, handler, "/api/skills/released-skill/quality/1", member)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body)
	}
	var body struct {
		Report json.RawMessage `json:"report"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Report) == 0 || string(body.Report) == "null" {
		t.Fatalf("report = %q, want the stored report on a released version for any caller", body.Report)
	}
}

// seedRecordWithReport upserts a minimal scored record for skillID/version,
// with report set to the given raw JSON (nil for a row written before
// migration 015 added report_json).
func seedRecordWithReport(t *testing.T, qs *quality.Store, skillID string, version int, report json.RawMessage) {
	t.Helper()
	rec := quality.Record{
		SkillID: skillID, Version: version, SuiteRef: "r", Tier: "full", Headline: 74.2,
		Pillars: json.RawMessage(`{}`), PanelMatrix: json.RawMessage(`[]`),
		DriftBreakdown: json.RawMessage(`{}`), ModelPanel: json.RawMessage(`[]`),
		ScoredAt: time.Now(), ReportJSON: report,
	}
	if err := qs.Upsert(t.Context(), rec); err != nil {
		t.Fatal(err)
	}
}

func TestGetQualityVersion_ServesStoredReport(t *testing.T) {
	handler, qs, sk, _ := newQualityTestServer(t)
	created, err := sk.Create(t.Context(), "detail-skill", "detail-skill", "", "", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	// v1 released, unscored.
	if _, err := sk.CreateVersion(t.Context(), created.ID, "archive-v1.tar.gz", "cksum1", "",
		"", "body", json.RawMessage(`{}`), nil, json.RawMessage(`{}`), "tester", allowDecision()); err != nil {
		t.Fatal(err)
	}
	// v2 released and scored — the version this test asserts against.
	if _, err := sk.CreateVersion(t.Context(), created.ID, "archive-v2.tar.gz", "cksum2", "",
		"", "body", json.RawMessage(`{}`), nil, json.RawMessage(`{}`), "tester", allowDecision()); err != nil {
		t.Fatal(err)
	}
	seedRecordWithReport(t, qs, created.ID, 2,
		json.RawMessage(`{"schema_version":1,"headline":74.2,"tasks":[{"task_id":"t1"}]}`))

	rr := doGet(t, handler, "/api/skills/detail-skill/quality/2")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body)
	}
	var body struct {
		Version int             `json:"version"`
		Report  json.RawMessage `json:"report"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Version != 2 {
		t.Fatalf("version = %d, want 2", body.Version)
	}
	if len(body.Report) == 0 || string(body.Report) == "null" {
		t.Fatalf("report = %q, want the stored report", body.Report)
	}
}

// A row written before migration 015 must serve its aggregates with a null
// report, not a 500 and not a 404. The UI branches on this.
func TestGetQualityVersion_NullReportStillServesAggregates(t *testing.T) {
	handler, qs, sk, _ := newQualityTestServer(t)
	created, err := sk.Create(t.Context(), "legacy-skill", "legacy-skill", "", "", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	seedRecordWithReport(t, qs, created.ID, 1, nil)

	rr := doGet(t, handler, "/api/skills/legacy-skill/quality/1")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body)
	}
	var body struct {
		Headline float64         `json:"headline_score"`
		Report   json.RawMessage `json:"report"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if body.Headline == 0 {
		t.Fatal("aggregates missing on a record whose report is absent")
	}
	if string(body.Report) != "null" {
		t.Fatalf("report = %q, want null", body.Report)
	}
}

func TestGetQualityVersion_UnscoredVersionIs404(t *testing.T) {
	handler, _, sk, _ := newQualityTestServer(t)
	_, err := sk.Create(t.Context(), "never-scored", "never-scored", "", "", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}

	rr := doGet(t, handler, "/api/skills/never-scored/quality/1")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

// /quality/history must not be captured by the {version} route.
func TestGetQualityHistory_NotShadowedByVersionRoute(t *testing.T) {
	handler, qs, sk, _ := newQualityTestServer(t)
	created, err := sk.Create(t.Context(), "shadow-check", "shadow-check", "", "", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	seedRecordWithReport(t, qs, created.ID, 1, nil)

	rr := doGet(t, handler, "/api/skills/shadow-check/quality/history")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body)
	}
	var body struct {
		History []struct {
			Version int `json:"version"`
		} `json:"history"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("history route returned a non-history body: %v", err)
	}
	if len(body.History) != 1 {
		t.Fatalf("history length = %d, want 1", len(body.History))
	}
}
