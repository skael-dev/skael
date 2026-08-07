package suite_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/eval/llm/fake"
	"github.com/skael-dev/skael/internal/eval/spec"
	"github.com/skael-dev/skael/internal/eval/suite"
)

// fakeGateway is a minimal llm.Gateway for tests that only care what prompt
// was sent, not the fuller call-recording fake.Gateway does — GenerateN's
// tests assert on prompt content only.
type fakeGateway struct {
	reply      string
	lastPrompt string
}

func (g *fakeGateway) Complete(_ context.Context, r llm.Req) (llm.Res, error) {
	g.lastPrompt = r.Prompt
	return llm.Res{Text: g.reply, Model: "fake"}, nil
}

func (g *fakeGateway) ModelFor(llm.ModelClass) string { return "fake-strong" }

// testSpec is a minimal skill spec, distinct from suiteSpec only in name —
// GenerateN's tests care about the prompt's task count, not the spec's
// content.
func testSpec() *spec.SkillSpec {
	return suiteSpec()
}

// minimalSuiteJSON is the smallest generateResult that parses: one task with
// every required field, and empty trigger lists.
func minimalSuiteJSON(t *testing.T) string {
	t.Helper()
	return `{"tasks":[{"id":"t0","kind":"happy","prompt_md":"p","oracle":"o","verifier":"v"}],` +
		`"triggers":{"positive":[],"negative":[]}}`
}

func suiteSpec() *spec.SkillSpec {
	return &spec.SkillSpec{
		Name: "pdf-extract", Purpose: "Extract tables.",
		Description: "Extracts tables. Use when a PDF is mentioned.",
		Triggers:    []spec.TriggerPhrase{{Text: "extract tables from this pdf"}, {Text: "summarise this novel", Negative: true}},
		Steps:       []spec.Step{{ID: "s1", Action: "Run scripts/extract.py.", Postcondition: "out/tables.csv exists."}},
		TargetTier:  spec.TierMid,
	}
}

// tenTasks scripts a suite response with ten core tasks and a trigger set.
func tenTasks() string {
	var b []byte
	b = append(b, `{"tasks":[`...)
	for i := 0; i < 10; i++ {
		if i > 0 {
			b = append(b, ',')
		}
		kind := "variant"
		if i == 0 {
			kind = "happy"
		}
		b = append(b, `{"id":"t`...)
		b = append(b, byte('0'+i))
		b = append(b, `","kind":"`+kind+`","prompt_md":"Extract the tables.","oracle":"#!/bin/sh\nexit 0\n","verifier":"#!/bin/sh\ntest -s out/tables.csv\n"}`...)
	}
	b = append(b, `],"triggers":{"positive":["p1","p2","p3","p4","p5","p6","p7","p8"],`...)
	b = append(b, `"negative":["n1","n2","n3","n4","n5","n6","n7","n8"]}}`...)
	return string(b)
}

func TestGenerateN_AsksForTheRequestedCount(t *testing.T) {
	g := &fakeGateway{reply: minimalSuiteJSON(t)}
	if _, err := suite.GenerateN(context.Background(), g, testSpec(), 18); err != nil {
		t.Fatalf("GenerateN: %v", err)
	}
	if !strings.Contains(g.lastPrompt, "18 task packages") {
		t.Fatalf("prompt does not ask for 18 tasks:\n%s", g.lastPrompt)
	}
}

func TestGenerate_StillAsksForTen(t *testing.T) {
	// The authored path's size is unchanged; GenerateN is additive.
	g := &fakeGateway{reply: minimalSuiteJSON(t)}
	if _, err := suite.Generate(context.Background(), g, testSpec()); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !strings.Contains(g.lastPrompt, "10 task packages") {
		t.Fatalf("prompt does not ask for 10 tasks:\n%s", g.lastPrompt)
	}
}

func TestGenerate_ProducesTasksAndTriggers(t *testing.T) {
	s, err := suite.Generate(context.Background(), fake.New(tenTasks()), suiteSpec())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(s.Tasks) != 10 {
		t.Errorf("got %d tasks, want 10", len(s.Tasks))
	}
	if len(s.Triggers.Positive) != 8 || len(s.Triggers.Negative) != 8 {
		t.Errorf("trigger set = %d positive / %d negative, want 8 and 8",
			len(s.Triggers.Positive), len(s.Triggers.Negative))
	}
}

func TestGenerate_EveryTaskHasAnOracleAndVerifier(t *testing.T) {
	// The oracle gate is what separates a broken task from a broken skill. A
	// task with no oracle can never be validated, so it would silently blame
	// the skill for its own defects.
	s, err := suite.Generate(context.Background(), fake.New(tenTasks()), suiteSpec())
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range s.Tasks {
		if task.Oracle == "" {
			t.Errorf("task %s has no oracle", task.ID)
		}
		if task.Verifier == "" {
			t.Errorf("task %s has no verifier", task.ID)
		}
	}
}

func TestSplit_Is70_30AndDeterministic(t *testing.T) {
	s, _ := suite.Generate(context.Background(), fake.New(tenTasks()), suiteSpec())
	s.Split(42)

	var dev, holdout int
	for _, task := range s.Tasks {
		switch task.Split {
		case "dev":
			dev++
		case "holdout":
			holdout++
		default:
			t.Fatalf("task %s has split %q", task.ID, task.Split)
		}
	}
	if dev != 7 || holdout != 3 {
		t.Errorf("split = %d dev / %d holdout, want 7 and 3", dev, holdout)
	}

	// Same seed must produce the same split, or a re-run silently changes which
	// tasks the repair loop was allowed to see.
	again, _ := suite.Generate(context.Background(), fake.New(tenTasks()), suiteSpec())
	again.Split(42)
	for i := range s.Tasks {
		if s.Tasks[i].Split != again.Tasks[i].Split {
			t.Fatalf("split is not deterministic for a fixed seed at task %d", i)
		}
	}
}

func TestSplit_HoldoutIsNonEmptyForSmallSuites(t *testing.T) {
	// Rounding must never produce a zero-task holdout — the reported score is
	// the holdout score, so an empty holdout means no reportable result at
	// all.
	s := &suite.Suite{Tasks: []suite.TaskPkg{{ID: "a"}, {ID: "b"}}}
	s.Split(1)

	var holdout int
	for _, task := range s.Tasks {
		if task.Split == "holdout" {
			holdout++
		}
	}
	if holdout == 0 {
		t.Error("a 2-task suite produced an empty holdout")
	}
}

// TestSplit_PartitionsWithoutDroppingOrDuplicating asserts the split is a
// genuine partition, not just a count that happens to add up. A
// counting-only assertion (dev==7, holdout==3) would still pass even if a
// task were silently dropped (landing in neither set) or duplicated
// (counted in a set more than once), which would quietly shrink or
// double-count the suite and corrupt the holdout guarantee.
//
// This walks every task and requires its Split to be exactly "dev" or
// "holdout" (a dropped task, left with Split == "" or anything else, fails
// immediately here — checking presence in s.Tasks would not catch this,
// since Split never removes entries from the slice, only sets a field on
// them), tracks per-set membership to catch a task recorded twice within
// one set, and finally checks the union of dev+holdout IDs is exactly the
// original ID set — neither short nor long.
func TestSplit_PartitionsWithoutDroppingOrDuplicating(t *testing.T) {
	s, err := suite.Generate(context.Background(), fake.New(tenTasks()), suiteSpec())
	if err != nil {
		t.Fatal(err)
	}
	s.Split(99)

	original := make(map[string]bool, len(s.Tasks))
	for _, task := range s.Tasks {
		original[task.ID] = true
	}

	dev := make(map[string]bool)
	holdout := make(map[string]bool)
	for _, task := range s.Tasks {
		switch task.Split {
		case "dev":
			if dev[task.ID] {
				t.Fatalf("task %s appears more than once in dev", task.ID)
			}
			dev[task.ID] = true
		case "holdout":
			if holdout[task.ID] {
				t.Fatalf("task %s appears more than once in holdout", task.ID)
			}
			holdout[task.ID] = true
		default:
			t.Fatalf("task %s has split %q, want exactly one of dev or holdout — a dropped task "+
				"lands here with an empty or stray value", task.ID, task.Split)
		}
		if dev[task.ID] && holdout[task.ID] {
			t.Fatalf("task %s is recorded in both dev and holdout", task.ID)
		}
	}

	union := make(map[string]bool, len(dev)+len(holdout))
	for id := range dev {
		union[id] = true
	}
	for id := range holdout {
		union[id] = true
	}

	if len(union) != len(original) {
		t.Fatalf("union of dev+holdout has %d distinct tasks, want %d (the original set) — a task "+
			"was dropped or duplicated", len(union), len(original))
	}
	for id := range original {
		if !union[id] {
			t.Errorf("task %s from the original set is missing from dev/holdout", id)
		}
	}
}

func TestWriteAndLoad_RoundTripsSkillsBenchLayout(t *testing.T) {
	s, _ := suite.Generate(context.Background(), fake.New(tenTasks()), suiteSpec())
	s.Split(7)

	dir := t.TempDir()
	if err := s.Write(dir); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Layout per the SkillsBench convention.
	for _, rel := range []string{
		filepath.Join("tasks", "t0", "task.md"),
		filepath.Join("tasks", "t0", "oracle", "solve.sh"),
		filepath.Join("tasks", "t0", "verifier", "test.sh"),
		"triggers.yaml",
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Errorf("missing %s: %v", rel, err)
		}
	}

	got, err := suite.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Tasks) != len(s.Tasks) {
		t.Errorf("loaded %d tasks, want %d", len(got.Tasks), len(s.Tasks))
	}
	if len(got.Triggers.Positive) != len(s.Triggers.Positive) {
		t.Error("trigger set lost in round trip")
	}
	if got.Tasks[0].Split == "" {
		t.Error("split assignment lost in round trip")
	}
}

// TestWriteAndLoad_RoundTripsKind asserts the Kind field survives Write then
// Load, not just Split. Kind distinguishes a happy-path task from an edge
// case or a negative-trigger task, and a lost Kind would silently mislabel
// the report rather than error, so it needs its own assertion rather than
// riding along on the Split-round-trip test.
//
// This comment used to claim "a later stage weights them differently when
// scoring". Nothing does: Kind reaches report.TaskInput and is rendered as a
// pill, and no scoring path reads it. A negative-trigger task — where the
// skill is supposed *not* to fire — therefore counts toward Reliability
// exactly like a happy-path task. That may be worth changing, but the claim
// was describing an intention rather than the code.
//
// The second half proves the assertion actually discriminates: it breaks the
// on-disk tag Kind is carried by (the meta.yaml "kind:" key) and confirms
// Load then returns the wrong value, rather than the field surviving by
// accident (e.g. because the zero value happens to match).
func TestWriteAndLoad_RoundTripsKind(t *testing.T) {
	s, err := suite.Generate(context.Background(), fake.New(tenTasks()), suiteSpec())
	if err != nil {
		t.Fatal(err)
	}
	s.Split(7)

	dir := t.TempDir()
	if err := s.Write(dir); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := suite.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	var loadedT0 *suite.TaskPkg
	for i := range got.Tasks {
		if got.Tasks[i].ID == "t0" {
			loadedT0 = &got.Tasks[i]
		}
	}
	if loadedT0 == nil {
		t.Fatal("task t0 missing after round trip")
	}
	if loadedT0.Kind != "happy" {
		t.Fatalf("Kind = %q after round trip, want %q", loadedT0.Kind, "happy")
	}

	// Now break the tag Kind rides on and confirm Load no longer returns the
	// original value — proving the assertion above is not a false positive
	// that would pass even if Kind were silently dropped.
	metaPath := filepath.Join(dir, "tasks", "t0", "meta.yaml")
	if err := os.WriteFile(metaPath, []byte("kind: mutated\nsplit: dev\n"), 0o644); err != nil {
		t.Fatalf("mutating meta.yaml: %v", err)
	}

	mutated, err := suite.Load(dir)
	if err != nil {
		t.Fatalf("Load after mutation: %v", err)
	}
	var mutatedT0 *suite.TaskPkg
	for i := range mutated.Tasks {
		if mutated.Tasks[i].ID == "t0" {
			mutatedT0 = &mutated.Tasks[i]
		}
	}
	if mutatedT0 == nil {
		t.Fatal("task t0 missing after mutated round trip")
	}
	if mutatedT0.Kind != "mutated" {
		t.Errorf("Kind = %q after mutating meta.yaml's kind tag, want %q — the field is not "+
			"actually carried by that tag", mutatedT0.Kind, "mutated")
	}
}

// TestWriteAndLoad_RoundTripsEveryTaskField is the field-by-field audit: every
// TaskPkg field and every TriggerSet field is set to a distinguishable,
// non-zero value and checked for exact content after a Write/Load round
// trip, not just presence or length. A silently-lost field in the on-disk
// task package mis-scores a later sandbox run rather than erroring, so a
// count- or existence-only assertion (as the layout test above uses) is not
// enough on its own.
func TestWriteAndLoad_RoundTripsEveryTaskField(t *testing.T) {
	s := &suite.Suite{
		Tasks: []suite.TaskPkg{
			{
				ID:       "custom-task",
				Kind:     "edge",
				Split:    "holdout",
				PromptMD: "# Prompt\n\nDo the thing precisely.\n",
				EnvFrag:  "FROM ubuntu:24.04\nRUN apt-get update\n",
				Oracle:   "#!/bin/sh\necho solved > solved.txt\nexit 0\n",
				Verifier: "#!/bin/sh\ntest -f solved.txt\n",
			},
		},
		Triggers: suite.TriggerSet{
			Positive: []string{"positive one", "positive two"},
			Negative: []string{"negative one", "negative two"},
		},
	}

	dir := t.TempDir()
	if err := s.Write(dir); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := suite.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Tasks) != 1 {
		t.Fatalf("loaded %d tasks, want 1", len(got.Tasks))
	}

	want := s.Tasks[0]
	loaded := got.Tasks[0]

	if loaded.ID != want.ID {
		t.Errorf("ID = %q, want %q", loaded.ID, want.ID)
	}
	if loaded.Kind != want.Kind {
		t.Errorf("Kind = %q, want %q", loaded.Kind, want.Kind)
	}
	if loaded.Split != want.Split {
		t.Errorf("Split = %q, want %q", loaded.Split, want.Split)
	}
	if loaded.PromptMD != want.PromptMD {
		t.Errorf("PromptMD = %q, want %q", loaded.PromptMD, want.PromptMD)
	}
	if loaded.EnvFrag != want.EnvFrag {
		t.Errorf("EnvFrag = %q, want %q", loaded.EnvFrag, want.EnvFrag)
	}
	if loaded.Oracle != want.Oracle {
		t.Errorf("Oracle = %q, want %q", loaded.Oracle, want.Oracle)
	}
	if loaded.Verifier != want.Verifier {
		t.Errorf("Verifier = %q, want %q", loaded.Verifier, want.Verifier)
	}

	if !reflect.DeepEqual(got.Triggers.Positive, s.Triggers.Positive) {
		t.Errorf("Triggers.Positive = %v, want %v", got.Triggers.Positive, s.Triggers.Positive)
	}
	if !reflect.DeepEqual(got.Triggers.Negative, s.Triggers.Negative) {
		t.Errorf("Triggers.Negative = %v, want %v", got.Triggers.Negative, s.Triggers.Negative)
	}
}

// TestWriteAndLoad_RoundTripsEnvFrag covers EnvFrag specifically: the on-disk
// layout only writes environment/Dockerfile.frag when EnvFrag is non-empty,
// so both the non-empty and the empty case need their own assertions, and
// the non-empty case needs proof that Load is actually reading that exact
// file rather than happening to return the right string some other way.
func TestWriteAndLoad_RoundTripsEnvFrag(t *testing.T) {
	const frag = "FROM ubuntu:24.04\nRUN apt-get update\n"

	s := &suite.Suite{Tasks: []suite.TaskPkg{
		{ID: "with-env", Kind: "happy", Split: "dev", PromptMD: "p", Oracle: "o", Verifier: "v", EnvFrag: frag},
		{ID: "without-env", Kind: "happy", Split: "dev", PromptMD: "p", Oracle: "o", Verifier: "v"},
	}}

	dir := t.TempDir()
	if err := s.Write(dir); err != nil {
		t.Fatalf("Write: %v", err)
	}

	envPath := filepath.Join(dir, "tasks", "with-env", "environment", "Dockerfile.frag")
	if _, err := os.Stat(envPath); err != nil {
		t.Fatalf("environment fragment not written for a task with non-empty EnvFrag: %v", err)
	}
	// A task with an empty EnvFrag must get no environment/ directory at all —
	// not an empty stray file, no directory.
	noEnvDir := filepath.Join(dir, "tasks", "without-env", "environment")
	if _, err := os.Stat(noEnvDir); !os.IsNotExist(err) {
		t.Fatalf("environment/ was written for a task with an empty EnvFrag (stat err = %v)", err)
	}

	got, err := suite.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	byID := make(map[string]suite.TaskPkg, len(got.Tasks))
	for _, task := range got.Tasks {
		byID[task.ID] = task
	}

	if byID["with-env"].EnvFrag != frag {
		t.Errorf("EnvFrag = %q after round trip, want %q", byID["with-env"].EnvFrag, frag)
	}
	if byID["without-env"].EnvFrag != "" {
		t.Errorf("EnvFrag = %q for a task that never had one, want empty", byID["without-env"].EnvFrag)
	}

	// Discrimination: prove the round trip depends on the exact path
	// environment/Dockerfile.frag, rather than the assertion above passing by
	// accident (e.g. EnvFrag defaulting to the zero value that happens to be
	// empty and the non-empty case being read from somewhere else). Rename
	// the file the layout doc says Load must read, and confirm the round
	// trip now comes back empty instead of quietly finding the content
	// elsewhere.
	mutatedPath := filepath.Join(dir, "tasks", "with-env", "environment", "Dockerfile.frag.bak")
	if err := os.Rename(envPath, mutatedPath); err != nil {
		t.Fatalf("renaming fragment for the discrimination check: %v", err)
	}

	mutated, err := suite.Load(dir)
	if err != nil {
		t.Fatalf("Load after renaming the fragment: %v", err)
	}
	var mutatedFrag string
	for _, task := range mutated.Tasks {
		if task.ID == "with-env" {
			mutatedFrag = task.EnvFrag
		}
	}
	if mutatedFrag != "" {
		t.Errorf("EnvFrag = %q after moving environment/Dockerfile.frag away, want empty — Load "+
			"must depend on that exact path, not incidentally recover the content some other way",
			mutatedFrag)
	}
}

func TestWrite_ScriptsAreExecutable(t *testing.T) {
	s, _ := suite.Generate(context.Background(), fake.New(tenTasks()), suiteSpec())
	dir := t.TempDir()
	if err := s.Write(dir); err != nil {
		t.Fatal(err)
	}

	for _, rel := range []string{"oracle/solve.sh", "verifier/test.sh"} {
		info, err := os.Stat(filepath.Join(dir, "tasks", "t0", rel))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o111 == 0 {
			t.Errorf("%s is not executable (mode %v); the sandbox executes it directly", rel, info.Mode().Perm())
		}
	}
}

func TestWrite_RefusesTaskIDsThatEscapeTheDirectory(t *testing.T) {
	// Task IDs are model-authored and become directory names.
	s := &suite.Suite{Tasks: []suite.TaskPkg{{ID: "../escape", Oracle: "x", Verifier: "y", PromptMD: "z"}}}
	if err := s.Write(t.TempDir()); err == nil {
		t.Error("Write accepted a task ID containing a traversal")
	}
}

func TestDistractors_ShippedPackIsUsable(t *testing.T) {
	d, err := suite.Distractors()
	if err != nil {
		t.Fatalf("Distractors: %v", err)
	}
	if len(d) < 24 {
		t.Errorf("got %d distractors, want at least 24", len(d))
	}

	seen := map[string]bool{}
	tiers := map[string]bool{}
	for _, x := range d {
		if x.Name == "" || x.Description == "" {
			t.Errorf("distractor with an empty field: %+v", x)
		}
		if seen[x.Name] {
			t.Errorf("duplicate distractor name %q", x.Name)
		}
		seen[x.Name] = true
		tiers[x.Tier] = true
	}
	// Difficulty tiers are what make trigger precision meaningful: a pack of
	// obviously-irrelevant skills tests nothing.
	if len(tiers) < 2 {
		t.Errorf("distractors span %d difficulty tiers, want at least 2", len(tiers))
	}
}
