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
	if err := s.FinishRun(runID, store.RunOutcome{VerifierExit: 0, Status: "ok", ArtifactDir: "d"}); err != nil {
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
}

func TestMigration3_PreservesAnExistingWorkspace(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveSpec(demoSpec()); err != nil {
		t.Fatal(err)
	}
	if err := s.Cache().Put("k", "v"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopening applies pending migrations. A workspace authored before this
	// phase must keep its specs and its completion cache — losing the cache
	// re-asks every gateway call the author already paid for.
	s2, err := store.Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = s2.Close() }()
	if _, _, err := s2.LoadSpec("demo"); err != nil {
		t.Errorf("spec lost across migration: %v", err)
	}
	if v, ok, err := s2.Cache().Get("k"); err != nil || !ok || v != "v" {
		t.Errorf("cache lost across migration: %q %v %v", v, ok, err)
	}
	if _, err := s2.CreateEval(store.EvalRecord{Skill: "demo", Tier: "smoke", Status: "running", StartedAt: time.Now()}); err != nil {
		t.Errorf("migration 3 did not apply: %v", err)
	}
}

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
