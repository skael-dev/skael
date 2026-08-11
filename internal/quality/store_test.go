package quality_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/skael-dev/skael/internal/eval/report"
	"github.com/skael-dev/skael/internal/eval/suite"
	"github.com/skael-dev/skael/internal/evalsuite"
	"github.com/skael-dev/skael/internal/platform"
	"github.com/skael-dev/skael/internal/quality"
	"github.com/skael-dev/skael/internal/testutil"
)

func TestStore_UpsertThenLatestReturnsTheNewestRow(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupTestDB(t)
	s := quality.NewStore(pool)
	skillID := insertSkill(t, pool, "deploy-helper")
	base := quality.Record{SkillID: skillID, Version: 2, SuiteRef: "r", Tier: "full",
		Pillars: json.RawMessage(`{}`), PanelMatrix: json.RawMessage(`{}`),
		DriftBreakdown: json.RawMessage(`{}`), ModelPanel: json.RawMessage(`[]`)}

	base.Headline, base.ScoredAt = 50, time.Now().Add(-time.Hour)
	if err := s.Upsert(ctx, base); err != nil {
		t.Fatal(err)
	}
	base.Headline, base.ScoredAt, base.Verified = 80, time.Now(), true
	if err := s.Upsert(ctx, base); err != nil {
		t.Fatal(err)
	}
	got, err := s.Latest(ctx, skillID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got.Headline != 80 || !got.Verified {
		t.Fatalf("latest = %+v, want headline 80 verified", got)
	}
	hist, _ := s.History(ctx, skillID)
	if len(hist) != 2 {
		t.Fatalf("history = %d rows, want 2 — a re-score must not destroy the earlier measurement", len(hist))
	}
}

// Latest must be deterministic even when two rows share the same scored_at
// — realistic with timestamp truncation or two ingestions in one request.
// Without a secondary sort key, Postgres gives no guarantee which row comes
// back, making the quality badge flaky.
func TestStore_LatestIsDeterministicOnTiedScoredAt(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupTestDB(t)
	s := quality.NewStore(pool)
	skillID := insertSkill(t, pool, "deploy-helper")
	tied := time.Now().Truncate(time.Second)

	base := quality.Record{SkillID: skillID, Version: 5, SuiteRef: "r", Tier: "full",
		Pillars: json.RawMessage(`{}`), PanelMatrix: json.RawMessage(`[]`),
		DriftBreakdown: json.RawMessage(`{}`), ModelPanel: json.RawMessage(`[]`), ScoredAt: tied}

	base.Headline = 10
	if err := s.Upsert(ctx, base); err != nil {
		t.Fatal(err)
	}
	base.Headline = 20
	if err := s.Upsert(ctx, base); err != nil {
		t.Fatal(err)
	}

	first, err := s.Latest(ctx, skillID, 5)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		got, err := s.Latest(ctx, skillID, 5)
		if err != nil {
			t.Fatal(err)
		}
		if got.Headline != first.Headline {
			t.Fatalf("Latest returned a different row across repeated calls: %v then %v", first.Headline, got.Headline)
		}
	}
}

// The grader model must survive the round trip: report.Comparable treats it as
// one of the fields that determines whether two reports' scores are a fair
// comparison, alongside SuiteRef/EngineVersion/Tier/ModelPanel/PanelComplete
// which are already preserved.
func TestStore_GraderModelRoundTrips(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupTestDB(t)
	s := quality.NewStore(pool)
	skillID := insertSkill(t, pool, "deploy-helper")
	grader := "claude-sonnet-5"
	rec := quality.Record{SkillID: skillID, Version: 1, SuiteRef: "r", Tier: "full",
		Pillars: json.RawMessage(`{}`), PanelMatrix: json.RawMessage(`[]`),
		DriftBreakdown: json.RawMessage(`{}`), ModelPanel: json.RawMessage(`[]`),
		JudgeModel: &grader, ScoredAt: time.Now()}
	if err := s.Upsert(ctx, rec); err != nil {
		t.Fatal(err)
	}
	got, err := s.Latest(ctx, skillID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got.JudgeModel == nil || *got.JudgeModel != "claude-sonnet-5" {
		t.Fatalf("judge_model = %v, want claude-sonnet-5", got.JudgeModel)
	}
	hist, err := s.History(ctx, skillID)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 1 || hist[0].JudgeModel == nil || *hist[0].JudgeModel != "claude-sonnet-5" {
		t.Fatalf("history judge_model lost: %+v", hist)
	}
}

// FromReport's empty-array marshaling must also survive a real round trip
// through Postgres JSONB, not just json.Marshal in isolation.
func TestStore_EmptyPanelMatrixRoundTripsAsArray(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupTestDB(t)
	s := quality.NewStore(pool)
	skillID := insertSkill(t, pool, "deploy-helper")

	r := &report.Report{SchemaVersion: report.SchemaVersion, Skill: "x", SuiteRef: "r"}
	rec, err := quality.FromReport(r)
	if err != nil {
		t.Fatal(err)
	}
	rec.SkillID, rec.Version, rec.ScoredAt = skillID, 1, time.Now()

	if err := s.Upsert(ctx, rec); err != nil {
		t.Fatal(err)
	}
	got, err := s.Latest(ctx, skillID, 1)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT jsonb_array_length(panel_matrix) FROM skill_quality WHERE skill_id = $1`, skillID).Scan(&n); err != nil {
		t.Fatalf("panel_matrix is not a JSON array in the database: %v", err)
	}
	if n != 0 {
		t.Fatalf("panel_matrix array length = %d, want 0", n)
	}
	if string(got.PanelMatrix) != "[]" {
		t.Fatalf("panel_matrix round-tripped as %s, want []", got.PanelMatrix)
	}
}

func TestStore_CriticalForbidViolationsRoundTrips(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupTestDB(t)
	s := quality.NewStore(pool)
	skillID := insertSkill(t, pool, "deploy-helper")
	rec := quality.Record{SkillID: skillID, Version: 1, SuiteRef: "r", Tier: "full",
		Pillars: json.RawMessage(`{}`), PanelMatrix: json.RawMessage(`[]`),
		DriftBreakdown: json.RawMessage(`{}`), ModelPanel: json.RawMessage(`[]`),
		CriticalForbidViolations: 3, ScoredAt: time.Now()}
	if err := s.Upsert(ctx, rec); err != nil {
		t.Fatal(err)
	}
	got, err := s.Latest(ctx, skillID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got.CriticalForbidViolations != 3 {
		t.Fatalf("critical_forbid_violations = %d, want 3", got.CriticalForbidViolations)
	}
}

func TestStore_JudgeModelRoundTrips(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupTestDB(t)
	s := quality.NewStore(pool)
	skillID := insertSkill(t, pool, "deploy-helper")
	model := "claude-opus-5"
	rec := quality.Record{SkillID: skillID, Version: 1, SuiteRef: "r", Tier: "full",
		Pillars: json.RawMessage(`{}`), PanelMatrix: json.RawMessage(`[]`),
		DriftBreakdown: json.RawMessage(`{}`), ModelPanel: json.RawMessage(`[]`),
		JudgeModel: &model, ScoredAt: time.Now()}
	if err := s.Upsert(ctx, rec); err != nil {
		t.Fatal(err)
	}
	got, err := s.Latest(ctx, skillID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got.JudgeModel == nil || *got.JudgeModel != model {
		t.Fatalf("judge_model = %v, want %q", got.JudgeModel, model)
	}
}

// TestStore_AbsentJudgeModelRoundTripsAsNil is the pre-migration / no-judge
// case: a row that never recorded a judge model must read back nil, not "" —
// asReport's grouping decision (an absent judge is its own distinct value)
// depends on being able to tell "no judge recorded" apart from an empty
// string.
func TestStore_AbsentJudgeModelRoundTripsAsNil(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupTestDB(t)
	s := quality.NewStore(pool)
	skillID := insertSkill(t, pool, "deploy-helper")
	rec := quality.Record{SkillID: skillID, Version: 1, SuiteRef: "r", Tier: "full",
		Pillars: json.RawMessage(`{}`), PanelMatrix: json.RawMessage(`[]`),
		DriftBreakdown: json.RawMessage(`{}`), ModelPanel: json.RawMessage(`[]`),
		ScoredAt: time.Now()}
	if err := s.Upsert(ctx, rec); err != nil {
		t.Fatal(err)
	}
	got, err := s.Latest(ctx, skillID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got.JudgeModel != nil {
		t.Fatalf("judge_model = %v, want nil", *got.JudgeModel)
	}
}

func TestStore_GetVersion_RoundTripsReportJSON(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupTestDB(t)
	s := quality.NewStore(pool)
	skillID := insertSkill(t, pool, "report-roundtrip")

	raw := json.RawMessage(`{"schema_version":1,"skill":"report-roundtrip","headline":74.2}`)
	rec := quality.Record{SkillID: skillID, Version: 1, SuiteRef: "r", Tier: "full",
		Pillars: json.RawMessage(`{}`), PanelMatrix: json.RawMessage(`[]`),
		DriftBreakdown: json.RawMessage(`{}`), ModelPanel: json.RawMessage(`[]`),
		ScoredAt: time.Now(), ReportJSON: raw}
	if err := s.Upsert(ctx, rec); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := s.GetVersion(ctx, skillID, 1)
	if err != nil {
		t.Fatalf("GetVersion: %v", err)
	}
	if got == nil {
		t.Fatal("GetVersion returned nil for a version that exists")
	}
	var decoded map[string]any
	if err := json.Unmarshal(got.ReportJSON, &decoded); err != nil {
		t.Fatalf("stored report is not valid JSON: %v", err)
	}
	if decoded["headline"] != 74.2 {
		t.Fatalf("headline = %v, want 74.2", decoded["headline"])
	}
}

// A row written before migration 015 has no report. The read must yield a nil
// ReportJSON, not an error and not an empty-but-non-nil slice — the detail
// page branches on nil to render the aggregates-only view.
func TestStore_GetVersion_AbsentReportIsNil(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupTestDB(t)
	s := quality.NewStore(pool)
	skillID := insertSkill(t, pool, "report-absent")

	rec := quality.Record{SkillID: skillID, Version: 1, SuiteRef: "r", Tier: "full",
		Pillars: json.RawMessage(`{}`), PanelMatrix: json.RawMessage(`[]`),
		DriftBreakdown: json.RawMessage(`{}`), ModelPanel: json.RawMessage(`[]`),
		ScoredAt: time.Now()} // ReportJSON left nil
	if err := s.Upsert(ctx, rec); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := s.GetVersion(ctx, skillID, 1)
	if err != nil {
		t.Fatalf("GetVersion: %v", err)
	}
	if got.ReportJSON != nil {
		t.Fatalf("ReportJSON = %q, want nil", got.ReportJSON)
	}
}

func TestStore_GetVersion_MissingVersionIsNilNil(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	s := quality.NewStore(pool)
	skillID := insertSkill(t, pool, "report-missing")

	got, err := s.GetVersion(context.Background(), skillID, 99)
	if err != nil {
		t.Fatalf("GetVersion: %v", err)
	}
	if got != nil {
		t.Fatalf("got %+v, want nil for an unscored version", got)
	}
}

// TestStore_SuiteDerivedFollowsTheSuiteRecord pins that the served flag is a
// live read. It was a column stamped at report time. A later review cannot
// update that column, even though the review is the exact event that must
// clear the badge.
func TestStore_SuiteDerivedFollowsTheSuiteRecord(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupTestDB(t)
	store := quality.NewStore(pool)
	skillID := insertSkill(t, pool, "suite-derived")

	reg, suiteRef := insertDerivedSuite(t, pool, "suite-derived")

	rec := quality.Record{SkillID: skillID, Version: 1, SuiteRef: suiteRef, Tier: "full",
		Pillars: json.RawMessage(`{}`), PanelMatrix: json.RawMessage(`[]`),
		DriftBreakdown: json.RawMessage(`{}`), ModelPanel: json.RawMessage(`[]`),
		ScoredAt: time.Now()}
	if err := store.Upsert(ctx, rec); err != nil {
		t.Fatal(err)
	}

	rec2, err := store.Latest(ctx, skillID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !rec2.SuiteDerived {
		t.Fatal("suite_derived is false while the suite is derived")
	}

	if err := reg.MarkAuthored(ctx, pool, suiteRef); err != nil {
		t.Fatal(err)
	}

	rec2, err = store.Latest(ctx, skillID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if rec2.SuiteDerived {
		t.Error("suite_derived is still true after the suite was marked authored")
	}
}

// insertDerivedSuite registers a fixture suite for skillName. It then marks
// the suite derived. It returns the registry and the suite's ref. The caller
// needs the registry to flip that origin later.
func insertDerivedSuite(t *testing.T, pool *pgxpool.Pool, skillName string) (*evalsuite.Registry, string) {
	t.Helper()
	ctx := context.Background()

	st, err := platform.NewLocalStorage(t.TempDir())
	if err != nil {
		t.Fatalf("insertDerivedSuite: NewLocalStorage: %v", err)
	}
	reg := evalsuite.NewRegistry(pool, st)

	dir := t.TempDir()
	set := &suite.EvalSet{
		SkillName: skillName,
		Evals: []suite.Eval{
			{ID: 1, Prompt: "Do the thing.", Expectations: []string{"it did the thing"}},
		},
	}
	if err := suite.WriteEvalSet(dir, set); err != nil {
		t.Fatalf("insertDerivedSuite: WriteEvalSet: %v", err)
	}
	archive, err := evalsuite.PackDir(dir)
	if err != nil {
		t.Fatalf("insertDerivedSuite: PackDir: %v", err)
	}
	rec, err := reg.Put(ctx, skillName, archive, []evalsuite.Check{{TaskID: "t1", OK: true}}, 1, "test@example.com", nil)
	if err != nil {
		t.Fatalf("insertDerivedSuite: Put: %v", err)
	}
	if err := reg.MarkDerived(ctx, pool, rec.Ref); err != nil {
		t.Fatalf("insertDerivedSuite: MarkDerived: %v", err)
	}
	return reg, rec.Ref
}

// insertSkill creates the skills row the foreign key needs.
func insertSkill(t *testing.T, pool *pgxpool.Pool, name string) string {
	t.Helper()
	var id string
	err := pool.QueryRow(context.Background(),
		`INSERT INTO skills (name) VALUES ($1) RETURNING id`, name).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
