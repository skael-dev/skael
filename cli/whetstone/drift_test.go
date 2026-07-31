package whetstone_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	whetstone "github.com/skael-dev/skael/cli/whetstone"
	"github.com/skael-dev/skael/internal/eval/report"
	"github.com/skael-dev/skael/internal/eval/score"
	"github.com/skael-dev/skael/internal/eval/store"
)

func TestRunDrift_LatestResolvesToTheNewestEval(t *testing.T) {
	seedTwoEvals(t) // headline 40 then 70, in that order
	r, err := whetstone.RunDrift(context.Background(), "demo", "latest")
	if err != nil {
		t.Fatal(err)
	}
	if r.Headline != 70 {
		t.Errorf("headline = %v, want the newest eval's 70", r.Headline)
	}
}

func TestRunDrift_AnUnknownSkillSaysWhatToRun(t *testing.T) {
	seedTwoEvals(t)
	_, err := whetstone.RunDrift(context.Background(), "nonesuch", "latest")
	if err == nil {
		t.Fatal("RunDrift succeeded for a skill with no evals")
	}
	// "not found" leaves a reader guessing whether they mistyped the skill or
	// never ran an eval.
	if !strings.Contains(err.Error(), "whetstone eval") {
		t.Errorf("err = %v, want it to name the command that would produce a result", err)
	}
}

// seedTwoEvals sets up a fresh .whetstone workspace in the current test's
// working directory (via t.Chdir) with two stored evals and reports for
// skill "demo": headline 40, then headline 70, created in that order so
// "latest" must resolve to the second one.
func seedTwoEvals(t *testing.T) {
	t.Helper()

	dir := t.TempDir()
	t.Chdir(dir)

	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	seedEvalReport(t, st, "demo", 40)
	seedEvalReport(t, st, "demo", 70)
}

// seedEvalReport records one eval and its report for skill, with the given
// headline.
func seedEvalReport(t *testing.T, st *store.Store, skill string, headline float64) int64 {
	t.Helper()

	now := time.Now().UTC()
	evalID, err := st.CreateEval(store.EvalRecord{
		Skill: skill, SpecVersion: 1, Tier: "smoke", SuiteRef: "deadbeefcafe",
		EngineVersion: "test-engine", Seed: 1, StartedAt: now, Status: "running",
	})
	if err != nil {
		t.Fatalf("CreateEval: %v", err)
	}

	rep := &report.Report{
		SchemaVersion: report.SchemaVersion,
		Skill:         skill,
		SpecVersion:   1,
		Tier:          "smoke",
		SuiteRef:      "deadbeefcafe",
		EngineVersion: "test-engine",
		ModelPanel:    []report.PanelMember{{Agent: "claude-code", Model: "test-model", Class: "mid"}},
		PanelComplete: true,
		Headline:      headline,
		UpliftSource:  score.UpliftPassRate,
		Members: []report.MemberReport{
			{
				Member:  report.PanelMember{Agent: "claude-code", Model: "test-model", Class: "mid"},
				Healthy: true,
			},
		},
		StartedAt:  now,
		FinishedAt: now,
	}

	var buf bytes.Buffer
	if err := rep.Save(&buf); err != nil {
		t.Fatalf("Report.Save: %v", err)
	}
	if err := st.SaveReport(evalID, buf.Bytes(), store.ReportMeta{Headline: headline, PanelComplete: true}); err != nil {
		t.Fatalf("SaveReport: %v", err)
	}
	if err := st.FinishEval(evalID, "done"); err != nil {
		t.Fatalf("FinishEval: %v", err)
	}
	return evalID
}
