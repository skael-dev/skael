package contest

import (
	"encoding/json"
	"testing"

	"github.com/skael-dev/skael/internal/eval/report"
	"github.com/skael-dev/skael/internal/eval/runner"
	"github.com/skael-dev/skael/internal/eval/score"
	"github.com/skael-dev/skael/internal/eval/store"
)

func reportFor(skill string, passes map[string]int, attempts int) json.RawMessage {
	rep := &report.Report{
		SchemaVersion: report.SchemaVersion,
		Skill:         skill,
		SuiteRef:      "ref",
		ModelPanel:    []report.PanelMember{{Agent: "claude-code", Model: "lead"}},
	}
	for id, p := range passes {
		task := report.TaskReport{
			TaskID: id,
			Conditions: []report.ConditionReport{
				{Condition: store.Condition(runner.CondSkill), Model: "lead", Passes: p, Runs: attempts},
				// The baseline condition must never reach a contest: it is not
				// the candidate.
				{Condition: store.Condition(runner.CondBaseline), Model: "lead", Passes: attempts, Runs: attempts},
			},
		}
		if p < attempts {
			task.Grades = []report.GradeNote{{
				Model: "lead", Condition: store.Condition(runner.CondSkill),
				Expectations: []score.Expectation{{Text: "tags the release", Passed: false, Evidence: "no tag"}},
			}}
		}
		rep.Tasks = append(rep.Tasks, task)
	}
	raw, err := json.Marshal(rep)
	if err != nil {
		panic(err)
	}
	return raw
}

func TestDecideReports_CountsOnlyTheSkillConditionOnThePrimaryMember(t *testing.T) {
	c := &Contest{Candidates: []Candidate{{Label: "a@1"}, {Label: "b@1"}}}
	reports := map[string]json.RawMessage{
		"a@1": reportFor("a", map[string]int{"1": 3, "2": 3}, 3),
		"b@1": reportFor("b", map[string]int{"1": 0, "2": 0}, 3),
	}

	v, losses, err := decideReports(c, reports)
	if err != nil {
		t.Fatalf("decideReports: %v", err)
	}
	p := v.Pairings[0]
	if p.AWins != 2 || p.BWins != 0 {
		t.Errorf("got %d-%d, want 2-0 — the baseline condition must not count", p.AWins, p.BWins)
	}
	if len(losses) != 2 {
		t.Fatalf("%d losses recorded, want 2 for the losing candidate's tasks", len(losses))
	}
	for _, l := range losses {
		if l.Label != "b@1" || l.Missed[0] != "tags the release" {
			t.Errorf("loss = %+v, want b@1 missing the tag expectation", l)
		}
	}
}

// A verdict over a subset would name a winner that only ran because the others
// failed to.
func TestDecideReports_RefusesAMissingCandidate(t *testing.T) {
	c := &Contest{Candidates: []Candidate{{Label: "a@1"}, {Label: "b@1"}}}
	_, _, err := decideReports(c, map[string]json.RawMessage{
		"a@1": reportFor("a", map[string]int{"1": 3}, 3),
	})
	if err == nil {
		t.Fatal("decided a contest with one candidate's runs missing")
	}
}
