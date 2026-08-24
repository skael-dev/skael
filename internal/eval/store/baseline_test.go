package store_test

import (
	"testing"

	"github.com/skael-dev/skael/internal/eval/store"
)

var baselineKey = store.RunKey{TaskID: "t1", Agent: "claude-code", Model: "opus", Condition: "baseline", Attempt: 1}

// finishedBaseline records one finished baseline session, with a grade, under
// a fresh eval, and returns that eval's id.
func finishedBaseline(t *testing.T, s *store.Store, agentVersion string) int64 {
	t.Helper()
	evalID := newEval(t, s)
	runID, _, err := s.ClaimRun(evalID, baselineKey)
	if err != nil {
		t.Fatalf("ClaimRun: %v", err)
	}
	if err := s.FinishRun(runID, store.RunOutcome{
		Status: store.StatusOK, AgentVersion: agentVersion, DurationMS: 1234,
		ArtifactDir: "/tmp/artifacts/t1",
	}); err != nil {
		t.Fatalf("FinishRun: %v", err)
	}
	if err := s.SaveGrade(evalID, store.RunGrade{Key: baselineKey, Passed: 2, Total: 3, Doc: []byte(`[{"passed":true}]`)}); err != nil {
		t.Fatalf("SaveGrade: %v", err)
	}
	return evalID
}

// TestReusableBaseline_MatchesTheSameSuiteTaskAgentModelAndVersion is the
// saving: a baseline installs no skill, so re-running the same one against an
// unchanged suite spends a session on a measurement already taken.
func TestReusableBaseline_MatchesTheSameSuiteTaskAgentModelAndVersion(t *testing.T) {
	s := openStore(t)
	finishedBaseline(t, s, "2.1.0")

	rec, ok, err := s.ReusableBaseline("abc123", "2.1.0", baselineKey)
	if err != nil {
		t.Fatalf("ReusableBaseline: %v", err)
	}
	if !ok {
		t.Fatal("no reusable baseline found for an identical key")
	}
	if rec.Outcome.ArtifactDir != "/tmp/artifacts/t1" {
		t.Errorf("ArtifactDir = %q, want the prior run's", rec.Outcome.ArtifactDir)
	}
}

// TestReusableBaseline_ADifferentAgentVersionBlocksReuse pins the rule that
// makes reuse safe: the agent's own version is part of what a baseline
// measures.
func TestReusableBaseline_ADifferentAgentVersionBlocksReuse(t *testing.T) {
	s := openStore(t)
	finishedBaseline(t, s, "2.1.0")

	if _, ok, err := s.ReusableBaseline("abc123", "2.2.0", baselineKey); err != nil {
		t.Fatalf("ReusableBaseline: %v", err)
	} else if ok {
		t.Error("a baseline recorded under another agent version was offered for reuse")
	}
}

// TestReusableBaseline_ADifferentSuiteBlocksReuse pins the other half: the
// tasks a baseline ran against are what it measured.
func TestReusableBaseline_ADifferentSuiteBlocksReuse(t *testing.T) {
	s := openStore(t)
	finishedBaseline(t, s, "2.1.0")

	if _, ok, err := s.ReusableBaseline("other-suite", "2.1.0", baselineKey); err != nil {
		t.Fatalf("ReusableBaseline: %v", err)
	} else if ok {
		t.Error("a baseline from another suite was offered for reuse")
	}
}

// TestReusableBaseline_AFailedRunIsNotReusable keeps a session that produced
// no measurement out of the pool.
func TestReusableBaseline_AFailedRunIsNotReusable(t *testing.T) {
	s := openStore(t)
	evalID := newEval(t, s)
	runID, _, err := s.ClaimRun(evalID, baselineKey)
	if err != nil {
		t.Fatalf("ClaimRun: %v", err)
	}
	if err := s.FinishRun(runID, store.RunOutcome{Status: store.StatusError, AgentVersion: "2.1.0"}); err != nil {
		t.Fatalf("FinishRun: %v", err)
	}

	if _, ok, err := s.ReusableBaseline("abc123", "2.1.0", baselineKey); err != nil {
		t.Fatalf("ReusableBaseline: %v", err)
	} else if ok {
		t.Error("a failed baseline was offered for reuse")
	}
}

// TestCopyBaseline_CarriesTheRunAndItsGrade proves the copy is what makes the
// reused session score: a run row with no grade counts as a session that
// passed nothing.
func TestCopyBaseline_CarriesTheRunAndItsGrade(t *testing.T) {
	s := openStore(t)
	finishedBaseline(t, s, "2.1.0")
	prior, ok, err := s.ReusableBaseline("abc123", "2.1.0", baselineKey)
	if err != nil || !ok {
		t.Fatalf("ReusableBaseline: %v (found: %v)", err, ok)
	}

	target := newEval(t, s)
	if err := s.CopyBaseline(target, prior, baselineKey); err != nil {
		t.Fatalf("CopyBaseline: %v", err)
	}

	runs, err := s.Runs(target)
	if err != nil {
		t.Fatalf("Runs: %v", err)
	}
	if len(runs) != 1 || runs[0].Outcome.Status != store.StatusOK {
		t.Fatalf("copied runs = %+v, want one ok baseline", runs)
	}
	grades, err := s.Grades(target)
	if err != nil {
		t.Fatalf("Grades: %v", err)
	}
	if len(grades) != 1 || grades[0].Passed != 2 || grades[0].Total != 3 {
		t.Fatalf("copied grades = %+v, want the prior 2 of 3", grades)
	}
}
