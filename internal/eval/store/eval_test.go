package store_test

import (
	"testing"
	"time"

	"github.com/skael-dev/skael/internal/eval/store"
)

func newEval(t *testing.T, s *store.Store) int64 {
	t.Helper()
	id, err := s.CreateEval(store.EvalRecord{
		Skill: "demo", SpecVersion: 1, Tier: "full",
		SuiteRef: "abc123", EngineVersion: "test", Seed: 1,
		ModelPanel: []byte(`[{"agent":"claude-code","model":"opus"}]`),
		StartedAt:  time.Unix(1700000000, 0).UTC(), Status: "running",
	})
	if err != nil {
		t.Fatalf("CreateEval: %v", err)
	}
	return id
}

func TestClaimRun_IsIdempotentSoAnInterruptedEvalResumes(t *testing.T) {
	s := openStore(t)
	id := newEval(t, s)
	k := store.RunKey{TaskID: "t1", Agent: "claude-code", Model: "opus", Condition: "skill", Attempt: 1}

	runID, done, err := s.ClaimRun(id, k)
	if err != nil {
		t.Fatal(err)
	}
	if done {
		t.Fatal("a fresh run reported as already done")
	}
	if err := s.FinishRun(runID, store.RunOutcome{Status: "ok", ArtifactDir: "d"}); err != nil {
		t.Fatal(err)
	}

	// This is the whole resume mechanism: a 60-session tier interrupted at
	// session 50 must cost ten sessions to finish, not sixty.
	again, done, err := s.ClaimRun(id, k)
	if err != nil {
		t.Fatal(err)
	}
	if !done {
		t.Error("a finished run was not reported as done; resume would re-run it")
	}
	if again != runID {
		t.Errorf("claim returned a new row (%d vs %d) for the same key", again, runID)
	}
}

func TestClaimRun_AnUnfinishedClaimIsRetried(t *testing.T) {
	s := openStore(t)
	id := newEval(t, s)
	k := store.RunKey{TaskID: "t1", Agent: "claude-code", Model: "opus", Condition: "skill", Attempt: 1}

	if _, _, err := s.ClaimRun(id, k); err != nil {
		t.Fatal(err)
	}
	// A process killed mid-run leaves a claimed-but-unfinished row. Treating
	// that as done would silently drop a session from the denominator and
	// inflate every rate computed from it.
	_, done, err := s.ClaimRun(id, k)
	if err != nil {
		t.Fatal(err)
	}
	if done {
		t.Error("an unfinished claim reported as done")
	}
}

// A grade is stored per run key and replaces an earlier one, so a re-graded
// eval does not abort on a unique-constraint failure.
func TestSaveGrade_UpsertsOnTheRunKey(t *testing.T) {
	s := openStore(t)
	id := newEval(t, s)
	k := store.RunKey{TaskID: "1", Agent: "claude-code", Model: "sonnet", Condition: "skill", Attempt: 1}

	if err := s.SaveGrade(id, store.RunGrade{Key: k, Passed: 1, Total: 3, Doc: []byte(`[{"text":"a"}]`)}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveGrade(id, store.RunGrade{Key: k, Passed: 3, Total: 3, Doc: []byte(`[{"text":"a"}]`)}); err != nil {
		t.Fatal(err)
	}

	gs, err := s.Grades(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(gs) != 1 {
		t.Fatalf("%d grades, want 1 after an upsert", len(gs))
	}
	if gs[0].Passed != 3 || gs[0].Total != 3 {
		t.Errorf("grade = %d of %d, want the second write", gs[0].Passed, gs[0].Total)
	}
	if gs[0].Key != k {
		t.Errorf("key = %+v, want %+v", gs[0].Key, k)
	}
}

func TestRunDirIsUniquePerRunKey(t *testing.T) {
	s := openStore(t)
	id := newEval(t, s)
	a, err := s.RunDir("demo", id, store.RunKey{TaskID: "t1", Agent: "claude-code", Model: "opus", Condition: "skill", Attempt: 1})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.RunDir("demo", id, store.RunKey{TaskID: "t1", Agent: "claude-code", Model: "opus", Condition: "baseline", Attempt: 1})
	if err != nil {
		t.Fatal(err)
	}
	// Two runs sharing an artifact directory overwrite each other's transcript,
	// and the pair that gets compared is skill against baseline.
	if a == b {
		t.Errorf("skill and baseline share an artifact dir: %s", a)
	}

	// A hyphen inside Agent or Model must not shift the field boundary: the
	// shipped default panel's agent name is literally "claude-code", so this
	// is the normal shape, not an exotic one. Without per-component escaping,
	// {Agent:"claude-code", Model:"opus"} and {Agent:"claude", Model:"code-opus"}
	// both flatten to the same leaf and one run's transcript silently
	// overwrites the other's — and these are exactly the two runs (skill vs.
	// baseline, or two models) most likely to be compared against each other.
	c, err := s.RunDir("demo", id, store.RunKey{TaskID: "t1", Agent: "claude-code", Model: "opus", Condition: "skill", Attempt: 1})
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.RunDir("demo", id, store.RunKey{TaskID: "t1", Agent: "claude", Model: "code-opus", Condition: "skill", Attempt: 1})
	if err != nil {
		t.Fatal(err)
	}
	if c == d {
		t.Errorf("a hyphen in Agent/Model shifted the field boundary: %q vs %q collided at %s", "claude-code/opus", "claude/code-opus", c)
	}

	// A component containing an underscore must not collide either: the fix
	// for the hyphen case joins fields with "__", so an underscore in a field
	// must itself be neutralized or it recreates the same ambiguity one level
	// down.
	e, err := s.RunDir("demo", id, store.RunKey{TaskID: "t1", Agent: "claude_code", Model: "opus", Condition: "skill", Attempt: 1})
	if err != nil {
		t.Fatal(err)
	}
	f, err := s.RunDir("demo", id, store.RunKey{TaskID: "t1", Agent: "claude", Model: "code_opus", Condition: "skill", Attempt: 1})
	if err != nil {
		t.Fatal(err)
	}
	if e == f {
		t.Errorf("an underscore in Agent/Model shifted the field boundary: %q vs %q collided at %s", "claude_code/opus", "claude/code_opus", e)
	}
}

// TestMigration3_PreservesAnExistingWorkspace and
// TestMigration9_PreservesAnExistingWorkspace live in migration_internal_test.go
// (package store, not store_test): they need direct access to the unexported
// migrations slice to build a database at an arbitrary prior schema version,
// rather than going through store.Open, which always applies every migration
// and so never leaves anything for the migration under test to actually do.

func TestReport_RoundTripsAndLatestFollowsTheNewestEval(t *testing.T) {
	s := openStore(t)
	first := newEval(t, s)
	if err := s.SaveReport(first, []byte(`{"headline":40}`), store.ReportMeta{Headline: 40, PanelComplete: true}); err != nil {
		t.Fatal(err)
	}
	second := newEval(t, s)
	if err := s.SaveReport(second, []byte(`{"headline":70}`), store.ReportMeta{Headline: 70, PanelComplete: true}); err != nil {
		t.Fatal(err)
	}

	doc, id, err := s.LatestReport("demo")
	if err != nil {
		t.Fatal(err)
	}
	if id != second || string(doc) != `{"headline":70}` {
		t.Errorf("LatestReport = %d %s, want the newest eval's report", id, doc)
	}
}

func TestReportMeta_RoundTripsANilRobustnessGapAsNilNotZero(t *testing.T) {
	s := openStore(t)
	id := newEval(t, s)
	if err := s.SaveReport(id, []byte(`{"headline":40}`), store.ReportMeta{Headline: 40, PanelComplete: true}); err != nil {
		t.Fatal(err)
	}

	m, err := s.ReportMeta(id)
	if err != nil {
		t.Fatalf("ReportMeta: %v", err)
	}
	if m.RobustnessGap != nil {
		t.Errorf("RobustnessGap = %v, want nil for a report saved with no gap", *m.RobustnessGap)
	}
	if m.Headline != 40 || !m.PanelComplete {
		t.Errorf("ReportMeta = %+v, want headline 40 and panel_complete true", m)
	}
}

func TestReportMeta_RoundTripsAPresentRobustnessGap(t *testing.T) {
	s := openStore(t)
	id := newEval(t, s)
	gap := 12.5
	if err := s.SaveReport(id, []byte(`{"headline":40}`), store.ReportMeta{Headline: 40, PanelComplete: true, RobustnessGap: &gap}); err != nil {
		t.Fatal(err)
	}

	m, err := s.ReportMeta(id)
	if err != nil {
		t.Fatalf("ReportMeta: %v", err)
	}
	if m.RobustnessGap == nil || *m.RobustnessGap != gap {
		t.Errorf("RobustnessGap = %v, want %v", m.RobustnessGap, gap)
	}
}

func TestScores_RoundTripsTheHealthyFlag(t *testing.T) {
	s := openStore(t)
	id := newEval(t, s)
	if err := s.SaveScore(id, store.ScoreRow{Agent: "claude-code", Model: "opus", Effectiveness: 82, Healthy: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveScore(id, store.ScoreRow{Agent: "claude-code", Model: "haiku", Effectiveness: 40, Healthy: false}); err != nil {
		t.Fatal(err)
	}

	rows, err := s.Scores(id)
	if err != nil {
		t.Fatalf("Scores: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("Scores returned %d rows, want 2", len(rows))
	}
	got := map[string]bool{}
	for _, r := range rows {
		got[r.Model] = r.Healthy
	}
	if !got["opus"] {
		t.Errorf("opus Healthy = false, want true")
	}
	if got["haiku"] {
		t.Errorf("haiku Healthy = true, want false")
	}
}
