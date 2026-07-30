package suite_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/skael-dev/skael/internal/eval/llm/fake"
	"github.com/skael-dev/skael/internal/eval/spec"
	"github.com/skael-dev/skael/internal/eval/suite"
)

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
// task were silently dropped and another duplicated across both sets, which
// would quietly shrink the suite and corrupt the holdout guarantee.
func TestSplit_PartitionsWithoutDroppingOrDuplicating(t *testing.T) {
	s, err := suite.Generate(context.Background(), fake.New(tenTasks()), suiteSpec())
	if err != nil {
		t.Fatal(err)
	}
	s.Split(99)

	original := make(map[string]bool)
	for _, task := range s.Tasks {
		original[task.ID] = true
	}

	seen := make(map[string]string) // id -> split
	for _, task := range s.Tasks {
		if prevSplit, ok := seen[task.ID]; ok {
			t.Fatalf("task %s appears more than once (splits %q and %q)", task.ID, prevSplit, task.Split)
		}
		seen[task.ID] = task.Split
	}

	if len(seen) != len(original) {
		t.Fatalf("union of dev+holdout has %d tasks, want %d — a task was dropped", len(seen), len(original))
	}
	for id := range original {
		if _, ok := seen[id]; !ok {
			t.Errorf("task %s from the original set is missing from the split", id)
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
// case or a negative-trigger task, and a later stage weights them
// differently when scoring — a lost Kind would mis-score silently rather
// than error, so it needs its own assertion rather than riding along on the
// Split-round-trip test.
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
