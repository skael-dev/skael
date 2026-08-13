package whetstone

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/eval/llm/fake"
	"github.com/skael-dev/skael/internal/eval/spec"
	"github.com/skael-dev/skael/internal/eval/store"
	"github.com/skael-dev/skael/internal/eval/suite"
	"github.com/skael-dev/skael/internal/eval/tune"
)

// TestRunTuneWith_WritesTheWinnerToTheSpecAndTheBundle pins the write-back
// order. A patch to SKILL.md alone does not survive the next `whetstone gen`,
// because the generator writes the frontmatter from the spec.
//
// The tuned description must genuinely win the held-out half. A fake that
// answers the same way for both descriptions gives them equal scores, and
// better() then breaks the tie toward the first attempt: res.Best comes back
// as the fixture's own description and every assertion below passes with the
// write-back removed. So the test asks Split for the same halves the loop
// uses. It drives the fake from that membership, the way
// internal/eval/tune/loop_test.go does.
func TestRunTuneWith_WritesTheWinnerToTheSpecAndTheBundle(t *testing.T) {
	st, skillDir, _ := tuneWorkspace(t)

	const (
		skill = "pdf-extract"
		tuned = "the tuned description"
	)
	_, versionBefore, err := st.LoadSpec(skill)
	if err != nil {
		t.Fatal(err)
	}
	queries := fixtureQueries()
	// The same seed and holdout RunTuneWith passes to tune.Run.
	train, _ := tune.Split(queries, 0.4, 42)
	inTrain := map[string]bool{}
	for _, q := range train {
		inTrain[q.Query] = true
	}
	positive := map[string]bool{}
	for _, q := range queries {
		positive[q.Query] = q.ShouldTrigger
	}
	// One train query always fails under the fixture's description. Without
	// it the train half is clean, the loop exits at iteration 1, and it never
	// proposes a second description to choose between.
	stumble := train[0].Query

	g := fake.NewFunc(func(r llm.Req) (string, error) {
		switch r.Role {
		case "tune.improve", "tune.shorten":
			return `{"description":"` + tuned + `"}`, nil
		}
		var q string
		for candidate := range positive {
			if strings.Contains(r.Prompt, "\n\n"+candidate+"\n\n") {
				q = candidate
				break
			}
		}
		if q == "" {
			return "", fmt.Errorf("the prompt carries no known query")
		}
		// The fixture's description answers the train half correctly and the
		// held-out half wrongly. The tuned one does the opposite, so it loses
		// the half that tunes and wins the half that decides.
		usesTuned := strings.Contains(r.Prompt, skill+": "+tuned)
		pass := usesTuned != inTrain[q]
		if !usesTuned && q == stumble {
			pass = false
		}
		if positive[q] == pass {
			return `{"skill":"` + skill + `"}`, nil
		}
		return `{"skill":"none"}`, nil
	})

	res, err := RunTuneWith(context.Background(), st, g, TuneRequest{
		Skill: skill, Queries: 4, Runs: 1, Iterations: 2,
		Threshold: 0.5, Holdout: 0.4, Apply: true,
	})
	if err != nil {
		t.Fatalf("RunTuneWith: %v", err)
	}

	// Guard the fixture itself. If the tuned description stops winning the
	// held-out half, this test measures nothing and must be repaired.
	if len(res.History) != 2 {
		t.Fatalf("ran %d iterations, want 2; the loop never proposed a second description", len(res.History))
	}
	if res.History[1].Test.Passed <= res.History[0].Test.Passed {
		t.Fatalf("the fixture is broken: %q did not win the held-out half", tuned)
	}
	if res.Best != tuned {
		t.Fatalf("Run chose %q, want the tuned description", res.Best)
	}

	sp, version, err := st.LoadSpec(skill)
	if err != nil {
		t.Fatal(err)
	}
	if version <= versionBefore {
		t.Errorf("spec version is %d, want more than the fixture's %d: the winner was never stored", version, versionBefore)
	}
	if sp.Description != res.Best {
		t.Errorf("spec description = %q, want the winner %q", sp.Description, res.Best)
	}
	if !isApproved(st, skill, version) {
		t.Error("the new spec version is not approved, so whetstone eval would refuse it")
	}

	md, err := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(md), res.Best) {
		t.Errorf("SKILL.md does not carry the winner:\n%s", md)
	}
}

// TestRunTuneWith_WritesTheEnlargedQuerySetBack pins that a top-up is
// persisted. The eval tiers read the same file, so a set grown only in
// memory helps nothing but this one run.
func TestRunTuneWith_WritesTheEnlargedQuerySetBack(t *testing.T) {
	st, _, suiteDir := tuneWorkspace(t)

	g := fake.NewFunc(func(r llm.Req) (string, error) {
		switch r.Role {
		case "tune.queries":
			return `{"queries":[
			  {"query":"convert the tables in acme_q4.pdf to csv","should_trigger":true},
			  {"query":"sort the rows of leads.csv by created date","should_trigger":false},
			  {"query":"turn the invoice pdf into a spreadsheet","should_trigger":true},
			  {"query":"write me a haiku about pdfs","should_trigger":false}
			]}`, nil
		case "tune.improve", "tune.shorten":
			return `{"description":"the tuned description"}`, nil
		default:
			return `{"skill":"none"}`, nil
		}
	})

	if _, err := RunTuneWith(context.Background(), st, g, TuneRequest{
		Skill: "pdf-extract", Queries: 6, Runs: 1, Iterations: 1,
		Threshold: 0.5, Holdout: 0.4, Apply: true,
	}); err != nil {
		t.Fatalf("RunTuneWith: %v", err)
	}

	qs, err := suite.LoadTriggerQueries(suiteDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(qs) != 6 {
		t.Errorf("triggers.json holds %d queries, want the 6 the tuner grew it to", len(qs))
	}
}

// TestRunTuneWith_RecordsTheGrownEvalSetRef is the laundering guard. A
// top-up writes into the directory suite.Ref hashes. A recorded ref that
// still names the pre-tune content makes the next `whetstone suite push`
// find a mismatch, declare nothing, and let the server record the eval set
// as authored. Nobody read it, and its score can then clear a scan hold.
func TestRunTuneWith_RecordsTheGrownEvalSetRef(t *testing.T) {
	st, _, suiteDir := tuneWorkspace(t)

	before, err := suite.Ref(suiteDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.RecordGeneratedRef("pdf-extract", before); err != nil {
		t.Fatal(err)
	}

	g := fake.NewFunc(func(r llm.Req) (string, error) {
		switch r.Role {
		case "tune.queries":
			return `{"queries":[
			  {"query":"convert the tables in acme_q4.pdf to csv","should_trigger":true},
			  {"query":"sort the rows of leads.csv by created date","should_trigger":false}
			]}`, nil
		case "tune.improve", "tune.shorten":
			return `{"description":"the tuned description"}`, nil
		default:
			return `{"skill":"none"}`, nil
		}
	})

	if _, err := RunTuneWith(context.Background(), st, g, TuneRequest{
		Skill: "pdf-extract", Queries: 6, Runs: 1, Iterations: 1,
		Threshold: 0.5, Holdout: 0.4, Apply: true,
	}); err != nil {
		t.Fatalf("RunTuneWith: %v", err)
	}

	after, err := suite.Ref(suiteDir)
	if err != nil {
		t.Fatal(err)
	}
	if after == before {
		t.Fatal("the fixture is broken: the top-up did not change the eval set")
	}

	got, err := st.GeneratedRef("pdf-extract")
	if err != nil {
		t.Fatal(err)
	}
	if got != after {
		t.Errorf("recorded ref = %q, want the grown set's ref %q; a push would declare this set authored", got, after)
	}
}

// TestRunTuneWith_ApplyFalseChangesNothing covers the dry run. It asks for
// more queries than the fixture holds, so a top-up genuinely fires. It
// checks that none of the three writes RunTuneWith can make land on disk.
func TestRunTuneWith_ApplyFalseChangesNothing(t *testing.T) {
	st, skillDir, suiteDir := tuneWorkspace(t)

	_, versionBefore, err := st.LoadSpec("pdf-extract")
	if err != nil {
		t.Fatal(err)
	}

	g := fake.NewFunc(func(r llm.Req) (string, error) {
		switch r.Role {
		case "tune.queries":
			return `{"queries":[
			  {"query":"convert the tables in acme_q4.pdf to csv","should_trigger":true},
			  {"query":"sort the rows of leads.csv by created date","should_trigger":false}
			]}`, nil
		case "tune.improve", "tune.shorten":
			return `{"description":"the tuned description"}`, nil
		default:
			return `{"skill":"none"}`, nil
		}
	})

	if _, err := RunTuneWith(context.Background(), st, g, TuneRequest{
		Skill: "pdf-extract", Queries: 6, Runs: 1, Iterations: 2,
		Threshold: 0.5, Holdout: 0.4, Apply: false,
	}); err != nil {
		t.Fatal(err)
	}

	md, err := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(md), "the tuned description") {
		t.Error("a dry run wrote the winner to SKILL.md")
	}

	qs, err := suite.LoadTriggerQueries(suiteDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(qs) != 4 {
		t.Errorf("triggers.json holds %d queries, want the original 4: a dry run must not persist a top-up", len(qs))
	}

	_, versionAfter, err := st.LoadSpec("pdf-extract")
	if err != nil {
		t.Fatal(err)
	}
	if versionAfter != versionBefore {
		t.Errorf("spec version changed from %d to %d: a dry run must not store a new version", versionBefore, versionAfter)
	}
}

// tuneWorkspace builds a store that holds one approved spec, a bundle, and
// a trigger set of four queries.
func tuneWorkspace(t *testing.T) (*store.Store, string, string) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	sp := &spec.SkillSpec{
		Name:        "pdf-extract",
		Purpose:     "Extract tables from PDFs.",
		Description: "old description",
		Triggers:    []spec.TriggerPhrase{{Text: "extract tables from this pdf"}},
		Steps:       []spec.Step{{ID: "s1", Action: "Run the script", Postcondition: "out/tables.csv exists"}},
		TargetTier:  spec.TierMid,
	}
	version, err := st.SaveSpec(sp)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ApproveSpec(sp.Name, version); err != nil {
		t.Fatal(err)
	}

	skillDir, err := st.SkillDir(sp.Name)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	md := "---\nname: pdf-extract\ndescription: old description\n---\n\n# PDF Extract\n\nThe body.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}

	suiteDir, err := st.SuiteDir(sp.Name)
	if err != nil {
		t.Fatal(err)
	}
	if err := suite.WriteTriggerQueries(suiteDir, fixtureQueries()); err != nil {
		t.Fatal(err)
	}
	return st, skillDir, suiteDir
}

// fixtureQueries is the trigger set tuneWorkspace writes. A test that has to
// know which half a query lands in reads it from here, so the two cannot
// drift apart.
func fixtureQueries() []suite.TriggerQuery {
	return []suite.TriggerQuery{
		{Query: "extract the tables from report.pdf", ShouldTrigger: true},
		{Query: "get the numbers out of this scanned invoice", ShouldTrigger: true},
		{Query: "clean up the duplicate rows in contacts.csv", ShouldTrigger: false},
		{Query: "summarise this markdown file for me", ShouldTrigger: false},
	}
}
