package whetstone

import (
	"testing"

	"github.com/skael-dev/skael/internal/eval/score"
	"github.com/skael-dev/skael/internal/eval/store"
)

// TestObservedGraderModel_ReportsTheOneModelThatGraded covers the ordinary
// subscription run, where every grade call went to the same model.
func TestObservedGraderModel_ReportsTheOneModelThatGraded(t *testing.T) {
	graded := map[store.RunKey]score.Grade{
		{TaskID: "1"}: {Model: "claude-sonnet-4-5"},
		{TaskID: "2"}: {Model: "claude-sonnet-4-5"},
	}
	if got := observedGraderModel(graded); got != "claude-sonnet-4-5" {
		t.Errorf("observedGraderModel = %q, want claude-sonnet-4-5", got)
	}
}

// TestObservedGraderModel_JoinsTwoModels pins what a mixed run reports. A CLI
// that answered two grade calls with two models produced one score from two
// judges. Report.Comparable then refuses to chart it beside a single-judge
// score, which is the honest outcome. A first-wins rule hides it.
func TestObservedGraderModel_JoinsTwoModels(t *testing.T) {
	graded := map[store.RunKey]score.Grade{
		{TaskID: "1"}: {Model: "sonnet"},
		{TaskID: "2"}: {Model: "haiku"},
	}
	if got := observedGraderModel(graded); got != "haiku, sonnet" {
		t.Errorf("observedGraderModel = %q, want a sorted join", got)
	}
}

// TestObservedGraderModel_IgnoresGradesWithNoModel covers a gateway that names
// nothing. An empty entry must not become an empty name inside a join.
func TestObservedGraderModel_IgnoresGradesWithNoModel(t *testing.T) {
	graded := map[store.RunKey]score.Grade{
		{TaskID: "1"}: {Model: ""},
		{TaskID: "2"}: {Model: "sonnet"},
	}
	if got := observedGraderModel(graded); got != "sonnet" {
		t.Errorf("observedGraderModel = %q, want sonnet", got)
	}
}

func TestObservedGraderModel_EmptyWhenNothingReported(t *testing.T) {
	if got := observedGraderModel(map[store.RunKey]score.Grade{}); got != "" {
		t.Errorf("observedGraderModel = %q, want the empty string", got)
	}
}
