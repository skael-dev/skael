package store

import (
	"testing"
)

// TestReusableBaseline_ARowOlderThanTheWindowBlocksReuse guards the staleness
// rule. An agent drifts even at a pinned version, so an old baseline compares
// a fresh skill run against a different world. The row is aged through the
// database directly, which is why this test is in the package.
func TestReusableBaseline_ARowOlderThanTheWindowBlocksReuse(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	evalID, err := s.CreateEval(EvalRecord{Skill: "demo", Tier: "full", SuiteRef: "abc123", Status: "running"})
	if err != nil {
		t.Fatalf("CreateEval: %v", err)
	}
	k := RunKey{TaskID: "t1", Agent: "claude-code", Model: "opus", Condition: "baseline", Attempt: 1}
	runID, _, err := s.ClaimRun(evalID, k)
	if err != nil {
		t.Fatalf("ClaimRun: %v", err)
	}
	if err := s.FinishRun(runID, RunOutcome{Status: StatusOK, AgentVersion: "2.1.0"}); err != nil {
		t.Fatalf("FinishRun: %v", err)
	}

	if _, err := s.db.Exec(`UPDATE runs SET created_at = datetime('now', '-31 days') WHERE id = ?`, runID); err != nil {
		t.Fatalf("ageing the run: %v", err)
	}

	if _, ok, err := s.ReusableBaseline("abc123", "2.1.0", k); err != nil {
		t.Fatalf("ReusableBaseline: %v", err)
	} else if ok {
		t.Error("a baseline older than the reuse window was offered for reuse")
	}
}
