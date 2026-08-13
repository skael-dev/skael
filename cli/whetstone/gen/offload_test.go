package gen

import (
	"fmt"
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/spec"
)

// declarativeFiller pads a section past offloadTargetBytes on its own, using
// filler text rather than real prose — only its size and heading matter here.
func declarativeFiller(n int) string {
	return strings.Repeat("filler word ", n)
}

func TestOffloadOverBudgetSections_UnderBudgetIsNoOp(t *testing.T) {
	body := "# Title\n\n1. Do the thing. Postcondition: out/ exists.\n"

	got, refs := offloadOverBudgetSections(body, nil)
	if got != body {
		t.Errorf("body changed for an under-budget input:\n%s", got)
	}
	if refs != nil {
		t.Errorf("refs = %v, want nil", refs)
	}
}

func TestOffloadOverBudgetSections_NoDeclarativeSectionsIsNoOp(t *testing.T) {
	// Over budget, but every heading is a step section — nothing declarative
	// to move, so offload must leave the procedure alone.
	body := "# Title\n\n## Workflow\n\n1. " + declarativeFiller(5000) + "\n"

	got, refs := offloadOverBudgetSections(body, nil)
	if got != body {
		t.Error("body was changed even though it has no declarative section")
	}
	if refs != nil {
		t.Errorf("refs = %v, want nil (nothing offloadable)", refs)
	}
}

func TestOffloadOverBudgetSections_PicksLargestDeclarativeFirst(t *testing.T) {
	body := "# Title\n\n1. Do the thing. Postcondition: out/ exists.\n\n" +
		"If a checkpoint cannot be satisfied after one retry, stop and report state.\n\n" +
		"## Notes\n\n" + declarativeFiller(200) + "\n\n" +
		"## Rules and constraints\n\n" + declarativeFiller(5000) + "\n\n" +
		"## Examples\n\n" + declarativeFiller(200) + "\n"

	got, refs := offloadOverBudgetSections(body, nil)

	if len(got) > offloadTargetBytes {
		t.Errorf("offloaded body is still %d bytes, over the %d-byte target", len(got), offloadTargetBytes)
	}
	if len(refs) == 0 {
		t.Fatal("no references produced")
	}
	// The largest section (Rules and constraints) alone is well past the
	// target, so it must be the one moved, and moving it alone should be
	// enough — the two small sections must stay in the body.
	if refs[0].Path != "references/rules-and-constraints.md" {
		t.Errorf("first offloaded file = %q, want the largest section first", refs[0].Path)
	}
	if len(refs) != 1 {
		t.Errorf("offloaded %d files, want exactly 1 (the largest section alone clears the budget)", len(refs))
	}
	if !strings.Contains(got, "See [Rules and constraints](references/rules-and-constraints.md).") {
		t.Errorf("body has no pointer to the offloaded file:\n%s", got)
	}
	// Notes and Examples were never large enough alone to need offloading —
	// their content must still be in the body.
	if !strings.Contains(got, "## Notes\n\n"+declarativeFiller(200)) {
		t.Error("the small Notes section was offloaded even though it wasn't needed")
	}
	if !strings.Contains(refs[0].Content, "Rules and constraints") {
		t.Errorf("offloaded content lost the heading: %q", refs[0].Content[:min(60, len(refs[0].Content))])
	}
}

func TestOffloadOverBudgetSections_CapsAtMaxReferences(t *testing.T) {
	// One more declarative section than the cap allows, each big enough that
	// an uncapped offload would move every one of them. Counts derive from
	// spec.MaxReferences so raising the cap does not silently defeat this.
	var b strings.Builder
	b.WriteString("# Title\n\n1. Do the thing. Postcondition: out/ exists.\n\n")
	for i := 0; i <= spec.MaxReferences; i++ {
		fmt.Fprintf(&b, "## Notes %d\n\n%s\n\n", i, declarativeFiller(2000))
	}
	body := b.String()

	_, refs := offloadOverBudgetSections(body, nil)
	if len(refs) > spec.MaxReferences {
		t.Errorf("offloaded %d files, want at most spec.MaxReferences (%d)", len(refs), spec.MaxReferences)
	}
}

func TestOffloadOverBudgetSections_SlugCollisionsAreDisambiguated(t *testing.T) {
	// Two headings that slugify identically ("Notes" and "Notes!") must not
	// collide on references/notes.md.
	body := "# Title\n\n1. Do the thing. Postcondition: out/ exists.\n\n" +
		"## Notes\n\n" + declarativeFiller(3000) + "\n\n" +
		"## Notes!\n\n" + declarativeFiller(3000) + "\n\n" +
		"## Examples\n\n" + declarativeFiller(3000) + "\n"

	_, refs := offloadOverBudgetSections(body, nil)

	seen := map[string]bool{}
	for _, r := range refs {
		if seen[r.Path] {
			t.Fatalf("duplicate offloaded path %q: %v", r.Path, refs)
		}
		seen[r.Path] = true
	}
	if len(refs) >= 2 {
		if refs[0].Path == refs[1].Path {
			t.Errorf("colliding slugs produced the same path: %q", refs[0].Path)
		}
	}
}

func TestOffloadOverBudgetSections_AvoidsCollidingWithAnAlreadyPlannedReference(t *testing.T) {
	body := "# Title\n\n1. Do the thing. Postcondition: out/ exists.\n\n" +
		"## Notes\n\n" + declarativeFiller(5000) + "\n"
	existing := []resourceFile{{Path: "references/notes.md", Content: "model-authored notes"}}

	_, refs := offloadOverBudgetSections(body, existing)
	if len(refs) != 1 {
		t.Fatalf("want exactly 1 offloaded file, got %d", len(refs))
	}
	if refs[0].Path == "references/notes.md" {
		t.Errorf("offload collided with an already-planned reference file: %q", refs[0].Path)
	}
}
