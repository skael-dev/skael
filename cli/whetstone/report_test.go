package whetstone_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	whetstone "github.com/skael-dev/skael/cli/whetstone"
	"github.com/skael-dev/skael/internal/eval/report"
	"github.com/skael-dev/skael/internal/eval/store"
)

func TestRunReport_WritesTheHTMLBesideTheSkill(t *testing.T) {
	seedTwoEvals(t)
	path, err := whetstone.RunReport("demo", "latest", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Ext(path) != ".html" {
		t.Errorf("path = %q, want an HTML file", path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "demo") {
		t.Error("the rendered report does not name the skill")
	}
}

func TestRunReport_OpenUsesTheInjectedOpener(t *testing.T) {
	seedTwoEvals(t)
	var opened string
	// Injected rather than shelling out: a test that launches a browser is a
	// test nobody runs twice.
	if _, err := whetstone.RunReport("demo", "latest", true, func(p string) error {
		opened = p
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if opened == "" {
		t.Error("--open did not call the opener")
	}
}

// seedTwoEvals writes two finished evals for one skill into a store in the
// test's own working directory, so RunReport's store lookup finds them.
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

	member := report.PanelMember{Agent: "claude-code", Model: "sonnet", Class: "strong"}
	rep := &report.Report{
		SchemaVersion: report.SchemaVersion,
		Skill:         skill,
		SpecVersion:   1,
		Tier:          "smoke",
		SuiteRef:      "deadbeefcafe",
		EngineVersion: "test-engine",
		ModelPanel:    []report.PanelMember{member},
		PanelComplete: true,
		Headline:      headline,
		TriggerF1:     1,
		Members:       []report.MemberReport{{Member: member, Effectiveness: headline, Healthy: true}},
		StartedAt:     now,
		FinishedAt:    now,
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
