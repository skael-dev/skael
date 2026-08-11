package whetstone

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/eval/llm/fake"
	"github.com/skael-dev/skael/internal/eval/spec"
	"github.com/skael-dev/skael/internal/eval/store"
	"github.com/skael-dev/skael/internal/eval/suite"
)

// TestRunTuneWith_WritesTheWinnerToTheSpecAndTheBundle pins the write-back
// order. A patch to SKILL.md alone does not survive the next `whetstone gen`,
// because the generator writes the frontmatter from the spec.
func TestRunTuneWith_WritesTheWinnerToTheSpecAndTheBundle(t *testing.T) {
	st, skillDir, suiteDir := tuneWorkspace(t)

	g := fake.NewFunc(func(r llm.Req) (string, error) {
		switch r.Role {
		case "tune.improve", "tune.shorten":
			return `{"description":"the tuned description"}`, nil
		default:
			return `{"skill":"none"}`, nil
		}
	})

	res, err := RunTuneWith(context.Background(), st, g, TuneRequest{
		Skill: "pdf-extract", Queries: 4, Runs: 1, Iterations: 2,
		Threshold: 0.5, Holdout: 0.4, Apply: true,
	})
	if err != nil {
		t.Fatalf("RunTuneWith: %v", err)
	}

	sp, version, err := st.LoadSpec("pdf-extract")
	if err != nil {
		t.Fatal(err)
	}
	if sp.Description != res.Best {
		t.Errorf("spec description = %q, want the winner %q", sp.Description, res.Best)
	}
	if !isApproved(st, "pdf-extract", version) {
		t.Error("the new spec version is not approved, so whetstone eval would refuse it")
	}

	md, err := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(md), res.Best) {
		t.Errorf("SKILL.md does not carry the winner:\n%s", md)
	}
	_ = suiteDir
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
	if err := suite.WriteTriggerQueries(suiteDir, []suite.TriggerQuery{
		{Query: "extract the tables from report.pdf", ShouldTrigger: true},
		{Query: "get the numbers out of this scanned invoice", ShouldTrigger: true},
		{Query: "clean up the duplicate rows in contacts.csv", ShouldTrigger: false},
		{Query: "summarise this markdown file for me", ShouldTrigger: false},
	}); err != nil {
		t.Fatal(err)
	}
	return st, skillDir, suiteDir
}
