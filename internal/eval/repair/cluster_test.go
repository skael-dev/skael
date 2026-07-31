package repair_test

import (
	"testing"

	"github.com/skael-dev/skael/internal/eval/repair"
	"github.com/skael-dev/skael/internal/eval/suite"
)

func TestCluster_GroupsByRuleAndCountsAcrossTasksAndModels(t *testing.T) {
	cs := repair.Cluster([]repair.Failure{
		{Kind: "contract", ID: "s3", TaskID: "t1", Model: "opus", Detail: "step not observed"},
		{Kind: "contract", ID: "s3", TaskID: "t2", Model: "haiku", Detail: "step not observed"},
		{Kind: "contract", ID: "s3", TaskID: "t2", Model: "opus", Detail: "step not observed"},
		{Kind: "verifier", ID: "t5", TaskID: "t5", Model: "haiku", Detail: "exit 1"},
	})
	if len(cs) != 2 {
		t.Fatalf("%d clusters, want 2: %+v", len(cs), cs)
	}
	// Clustering is what turns sixty run failures into two things to fix. A
	// per-run list would send the repair prompt the same defect three times and
	// spend its diff budget saying it three ways.
	var s3 repair.FailureCluster
	for _, c := range cs {
		if c.ID == "s3" {
			s3 = c
		}
	}
	if s3.Count != 3 || len(s3.Tasks) != 2 || len(s3.Models) != 2 {
		t.Errorf("s3 cluster = %+v", s3)
	}
}

func TestCluster_OrdersByCountSoTheWorstIsFirst(t *testing.T) {
	cs := repair.Cluster([]repair.Failure{
		{Kind: "contract", ID: "a", TaskID: "t1"},
		{Kind: "contract", ID: "b", TaskID: "t1"},
		{Kind: "contract", ID: "b", TaskID: "t2"},
	})
	if cs[0].ID != "b" {
		t.Errorf("first cluster = %q, want the most frequent", cs[0].ID)
	}
}

func TestAuditAssertions_PartitionsIntoTheThreeCases(t *testing.T) {
	a := repair.AuditAssertions([]repair.ConditionResult{
		// fails with the skill, passes without: a real skill defect.
		{TaskID: "t1", Model: "opus", SkillPassed: false, BaselinePassed: true},
		// passes in both: measures nothing about the skill.
		{TaskID: "t2", Model: "opus", SkillPassed: true, BaselinePassed: true},
		// fails in both: a broken task.
		{TaskID: "t3", Model: "opus", SkillPassed: false, BaselinePassed: false},
		// passes with the skill, fails without: working as intended.
		{TaskID: "t4", Model: "opus", SkillPassed: true, BaselinePassed: false},
	})

	if len(a.NonDiscriminating) != 1 || a.NonDiscriminating[0] != "t2" {
		t.Errorf("NonDiscriminating = %v, want [t2]", a.NonDiscriminating)
	}
	// Routing a broken task to skill edits is how a repair loop spends three
	// iterations rewording instructions to satisfy a verifier that rejects its
	// own oracle.
	if len(a.BrokenInBoth) != 1 || a.BrokenInBoth[0] != "t3" {
		t.Errorf("BrokenInBoth = %v, want [t3]", a.BrokenInBoth)
	}
	if len(a.Discriminating) != 2 {
		t.Errorf("Discriminating = %v, want t1 and t4", a.Discriminating)
	}
}

func TestAuditAssertions_ATaskIsOnlyNonDiscriminatingIfEveryModelAgrees(t *testing.T) {
	a := repair.AuditAssertions([]repair.ConditionResult{
		{TaskID: "t1", Model: "opus", SkillPassed: true, BaselinePassed: true},
		{TaskID: "t1", Model: "haiku", SkillPassed: true, BaselinePassed: false},
	})
	// The floor model needed the skill; the strong model did not. That is the
	// RobustnessGap signal, not a useless task — pruning it would delete the
	// evidence that the skill matters where it matters most.
	if len(a.NonDiscriminating) != 0 {
		t.Errorf("NonDiscriminating = %v, want empty: the task discriminates on the floor model", a.NonDiscriminating)
	}
	if len(a.Discriminating) != 1 {
		t.Errorf("Discriminating = %v", a.Discriminating)
	}
}

func TestPruneTasks_RemovesNonDiscriminatingAndBrokenTasks(t *testing.T) {
	ts := []suite.TaskPkg{{ID: "t1"}, {ID: "t2"}, {ID: "t3"}, {ID: "t4"}}
	got := repair.PruneTasks(repair.Audit{
		NonDiscriminating: []string{"t2"},
		BrokenInBoth:      []string{"t3"},
	}, ts)

	// The prune has to have an effect on the next iteration's scoring, not just
	// appear in a list. A pruned check that still contributes to Reliability is
	// a report that says one thing and a number that says another.
	if len(got) != 2 {
		t.Fatalf("%d tasks after pruning, want 2: %+v", len(got), got)
	}
	for _, task := range got {
		if task.ID == "t2" || task.ID == "t3" {
			t.Errorf("task %s survived pruning", task.ID)
		}
	}
}
