package suite_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/spec"
	"github.com/skael-dev/skael/internal/eval/suite"
)

// writeEvalsJSON writes raw bytes to dir/evals/evals.json.
func writeEvalsJSON(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "evals"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "evals", "evals.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadEvalSet_ReadsAnthropicsShape(t *testing.T) {
	dir := t.TempDir()
	writeEvalsJSON(t, dir, `{
	  "skill_name": "pdf-extract",
	  "evals": [
	    {"id": 1, "prompt": "extract the tables", "expected_output": "a csv",
	     "files": ["evals/files/in.pdf"], "expectations": ["out/tables.csv exists"]}
	  ]
	}`)

	set, err := suite.LoadEvalSet(dir)
	if err != nil {
		t.Fatalf("LoadEvalSet: %v", err)
	}
	if set.SkillName != "pdf-extract" || len(set.Evals) != 1 {
		t.Fatalf("set = %+v", set)
	}
	e := set.Evals[0]
	if e.ID != 1 || e.Prompt != "extract the tables" || e.ExpectedOutput != "a csv" {
		t.Errorf("eval = %+v", e)
	}
	if len(e.Files) != 1 || len(e.Expectations) != 1 {
		t.Errorf("files = %v, expectations = %v", e.Files, e.Expectations)
	}
}

// Anthropic's own SKILL.md writes `assertions` where its schema reference and
// its grader both write `expectations`. A file from skill-creator must load.
func TestLoadEvalSet_AcceptsAssertionsAsAnAlias(t *testing.T) {
	dir := t.TempDir()
	writeEvalsJSON(t, dir, `{"skill_name":"s","evals":[
	  {"id":1,"prompt":"p","assertions":["the output names John Smith"]}]}`)

	set, err := suite.LoadEvalSet(dir)
	if err != nil {
		t.Fatalf("LoadEvalSet: %v", err)
	}
	if got := set.Evals[0].Expectations; len(got) != 1 || got[0] != "the output names John Smith" {
		t.Errorf("expectations = %v, want the assertions value", got)
	}
}

// A field this binary does not know must not fail the load, or a file written
// by a newer skill-creator becomes unreadable.
func TestLoadEvalSet_IgnoresUnknownFields(t *testing.T) {
	dir := t.TempDir()
	writeEvalsJSON(t, dir, `{"skill_name":"s","tags":["x"],"evals":[
	  {"id":1,"prompt":"p","expectations":["e"],"difficulty":"hard"}]}`)

	if _, err := suite.LoadEvalSet(dir); err != nil {
		t.Fatalf("LoadEvalSet: %v", err)
	}
}

func TestLoadEvalSet_RefusesADuplicateID(t *testing.T) {
	dir := t.TempDir()
	writeEvalsJSON(t, dir, `{"skill_name":"s","evals":[
	  {"id":1,"prompt":"a","expectations":["e"]},
	  {"id":1,"prompt":"b","expectations":["e"]}]}`)

	if _, err := suite.LoadEvalSet(dir); err == nil {
		t.Fatal("LoadEvalSet accepted two evals with the same id")
	}
}

func TestLoadEvalSet_RefusesAMissingPrompt(t *testing.T) {
	dir := t.TempDir()
	writeEvalsJSON(t, dir, `{"skill_name":"s","evals":[{"id":1,"expectations":["e"]}]}`)

	if _, err := suite.LoadEvalSet(dir); err == nil {
		t.Fatal("LoadEvalSet accepted an eval with no prompt")
	}
}

func TestEvalSet_RoundTripsThroughDisk(t *testing.T) {
	dir := t.TempDir()
	want := &suite.EvalSet{SkillName: "s", Evals: []suite.Eval{
		{ID: 1, Prompt: "p", Expectations: []string{"e1", "e2"}},
	}}
	if err := suite.WriteEvalSet(dir, want); err != nil {
		t.Fatalf("WriteEvalSet: %v", err)
	}
	got, err := suite.LoadEvalSet(dir)
	if err != nil {
		t.Fatalf("LoadEvalSet: %v", err)
	}
	if got.SkillName != want.SkillName || len(got.Evals) != 1 {
		t.Fatalf("got = %+v", got)
	}
	if len(got.Evals[0].Expectations) != 2 {
		t.Errorf("expectations = %v", got.Evals[0].Expectations)
	}

	// The written file must never carry the alias, whatever it was read from.
	b, err := os.ReadFile(filepath.Join(dir, "evals", "evals.json"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "\"expectations\""; !strings.Contains(string(b), want) {
		t.Errorf("written file has no %s field", want)
	}
	if bad := "assertions"; strings.Contains(string(b), bad) {
		t.Errorf("written file carries the %q alias", bad)
	}
}

func TestTriggerQueries_RoundTripAsABareArray(t *testing.T) {
	dir := t.TempDir()
	qs := []suite.TriggerQuery{
		{Query: "convert this pdf", ShouldTrigger: true},
		{Query: "rename these files", ShouldTrigger: false},
	}
	if err := suite.WriteTriggerQueries(dir, qs); err != nil {
		t.Fatalf("WriteTriggerQueries: %v", err)
	}

	// run_eval.py takes a bare array through --eval-set. An object wrapper here
	// would make the file ours rather than portable.
	b, err := os.ReadFile(filepath.Join(dir, "evals", "triggers.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(b) == 0 || b[0] != '[' {
		t.Errorf("triggers.json does not start with a bare array: %q", strings.SplitN(string(b), "\n", 2)[0])
	}

	got, err := suite.LoadTriggerQueries(dir)
	if err != nil {
		t.Fatalf("LoadTriggerQueries: %v", err)
	}
	if len(got) != 2 || got[0].Query != qs[0].Query || got[1].ShouldTrigger {
		t.Errorf("got = %+v", got)
	}
}

func TestLoadTriggerQueries_AbsentFileIsNotAnError(t *testing.T) {
	got, err := suite.LoadTriggerQueries(t.TempDir())
	if err != nil {
		t.Fatalf("LoadTriggerQueries: %v", err)
	}
	if got != nil {
		t.Errorf("got = %+v, want nil", got)
	}
}

func TestTriggersFromSpec_MapsNegativeToShouldNotTrigger(t *testing.T) {
	sp := &spec.SkillSpec{Triggers: []spec.TriggerPhrase{
		{Text: "extract this table"},
		{Text: "summarise this pdf", Negative: true},
	}}
	got := suite.TriggersFromSpec(sp)
	if len(got) != 2 {
		t.Fatalf("got %d queries, want 2", len(got))
	}
	if !got[0].ShouldTrigger {
		t.Error("a positive phrase mapped to should_trigger=false")
	}
	if got[1].ShouldTrigger {
		t.Error("a negative phrase mapped to should_trigger=true")
	}
}

func TestValidate_VoidsAnEvalWithNoExpectations(t *testing.T) {
	dir := t.TempDir()
	set := &suite.EvalSet{Evals: []suite.Eval{{ID: 1, Prompt: "p"}}}

	got := suite.Validate(dir, set)
	if len(got) != 1 || !got[0].Void {
		t.Fatalf("got = %+v, want one void check", got)
	}
	if got[0].Reason == "" {
		t.Error("a void eval reported no reason")
	}
}

func TestValidate_VoidsAnEvalWhoseInputFileIsAbsent(t *testing.T) {
	dir := t.TempDir()
	set := &suite.EvalSet{Evals: []suite.Eval{
		{ID: 1, Prompt: "p", Expectations: []string{"e"}, Files: []string{"evals/files/in.csv"}},
	}}

	got := suite.Validate(dir, set)
	if len(got) != 1 || !got[0].Void {
		t.Fatalf("got = %+v, want one void check", got)
	}

	if err := os.MkdirAll(filepath.Join(dir, "evals", "files"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "evals", "files", "in.csv"), []byte("a,b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got = suite.Validate(dir, set)
	if got[0].Void || !got[0].OK {
		t.Errorf("got = %+v, want an OK check once the file exists", got)
	}
}

// The files list is model-authored, so a path that climbs out of the skill
// directory must read as absent rather than resolve.
func TestValidate_VoidsAnEvalWhoseInputFileEscapesTheSkillDir(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(dir, "..", "escaped.csv")
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(outside)

	set := &suite.EvalSet{Evals: []suite.Eval{
		{ID: 1, Prompt: "p", Expectations: []string{"e"}, Files: []string{"../escaped.csv"}},
	}}
	if got := suite.Validate(dir, set); !got[0].Void {
		t.Errorf("got = %+v, want void for a path outside the skill directory", got)
	}
}
