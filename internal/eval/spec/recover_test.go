package spec_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/llm/fake"
	"github.com/skael-dev/skael/internal/eval/spec"
)

// writeFile creates a file at dir/name with the given content, creating
// intermediate directories as needed.
func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// newMinimalBundle creates a temporary directory with a valid SKILL.md.
func newMinimalBundle(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "SKILL.md", "---\nname: demo\ndescription: A demo skill\n---\n\nA demo.\n")
	return dir
}

// validSpecJSON returns a JSON string representing a valid SkillSpec.
func validSpecJSON(t *testing.T) string {
	t.Helper()
	s := &spec.SkillSpec{
		Name:        "demo",
		Purpose:     "A demo skill",
		Description: "This is a demo skill for testing.",
		Triggers: []spec.TriggerPhrase{
			{Text: "do something"},
			{Text: "something else", Negative: true},
		},
		Steps: []spec.Step{
			{ID: "s1", Action: "Do the thing", Postcondition: "The thing is done"},
		},
		TargetTier: spec.TierMid,
	}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	return string(b)
}

// validSpecJSONNamed returns a JSON string representing a valid SkillSpec with
// the given name. Note: the spec's Name field will be set to the given name,
// but Recover will override it anyway, so this is just for checking that the
// LLM's output is used if the name is not overridden.
func validSpecJSONNamed(t *testing.T, name string) string {
	t.Helper()
	s := &spec.SkillSpec{
		Name:        name,
		Purpose:     "A demo skill",
		Description: "This is a demo skill for testing.",
		Triggers: []spec.TriggerPhrase{
			{Text: "do something"},
			{Text: "something else", Negative: true},
		},
		Steps: []spec.Step{
			{ID: "s1", Action: "Do the thing", Postcondition: "The thing is done"},
		},
		TargetTier: spec.TierMid,
	}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	return string(b)
}

// invalidSpecJSON returns a JSON string representing an invalid SkillSpec
// (missing postcondition).
func invalidSpecJSON(t *testing.T) string {
	t.Helper()
	s := &spec.SkillSpec{
		Name:        "demo",
		Purpose:     "A demo skill",
		Description: "This is a demo skill for testing.",
		Triggers: []spec.TriggerPhrase{
			{Text: "do something"},
		},
		Steps: []spec.Step{
			{ID: "s1", Action: "Do the thing", Postcondition: ""}, // Invalid: missing postcondition
		},
		TargetTier: spec.TierMid,
	}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	return string(b)
}

func TestRecover_UsesBundleContentInThePrompt(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "SKILL.md", "---\nname: pdf-split\ndescription: Split PDFs\n---\n\nRun scripts/split.sh.\n")
	writeFile(t, dir, "scripts/split.sh", "#!/bin/bash\nqpdf --split-pages \"$1\" out/page.pdf\n")

	g := fake.New(validSpecJSON(t))
	if _, err := spec.Recover(context.Background(), g, "pdf-split", dir); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	// The whole point of reading the bundle rather than just SKILL.md is that
	// a skill's real behaviour often lives in its scripts.
	prompt := g.Calls()[len(g.Calls())-1].Prompt
	if !strings.Contains(prompt, "qpdf --split-pages") {
		t.Fatal("prompt does not include scripts/ content")
	}
	if !strings.Contains(prompt, "Run scripts/split.sh.") {
		t.Fatal("prompt does not include the SKILL.md body")
	}
}

func TestRecover_NamesTheSkillFromTheRegistry(t *testing.T) {
	// A namespaced registry name must survive: Validate checks DirName(),
	// which strips everything up to the last colon, so "superpowers:brainstorming"
	// is legal even though the raw name is not kebab-case.
	dir := newMinimalBundle(t)
	g := fake.New(validSpecJSONNamed(t, "something-else"))

	sp, err := spec.Recover(context.Background(), g, "superpowers:brainstorming", dir)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if sp.Name != "superpowers:brainstorming" {
		t.Fatalf("Name = %q, want the registry name", sp.Name)
	}
}

func TestRecover_RepairsAnInvalidDraft(t *testing.T) {
	// The second call is conditional. An invalid draft gets one repair pass,
	// told exactly what was wrong.
	dir := newMinimalBundle(t)
	g := fake.New(invalidSpecJSON(t), validSpecJSON(t))

	if _, err := spec.Recover(context.Background(), g, "demo", dir); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if n := len(g.Calls()); n != 2 {
		t.Fatalf("gateway called %d times, want 2 (draft + repair)", n)
	}
	if !strings.Contains(g.Calls()[1].Prompt, "postcondition") {
		t.Fatal("repair prompt does not name the validation failure")
	}
}

func TestRecover_ValidDraftCostsOneCall(t *testing.T) {
	// Interview's unconditional second call is a design critique. Recovering
	// an existing skill has no design to improve, so it is not paid for.
	dir := newMinimalBundle(t)
	g := fake.New(validSpecJSON(t))

	if _, err := spec.Recover(context.Background(), g, "demo", dir); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if n := len(g.Calls()); n != 1 {
		t.Fatalf("gateway called %d times for a valid draft, want 1", n)
	}
}

func TestRecover_TruncatesAFatBundle(t *testing.T) {
	dir := newMinimalBundle(t)
	writeFile(t, dir, "references/huge.md", strings.Repeat("x", 200_000))

	g := fake.New(validSpecJSON(t))
	if _, err := spec.Recover(context.Background(), g, "demo", dir); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	prompt := g.Calls()[len(g.Calls())-1].Prompt
	if len(prompt) > 80_000 {
		t.Fatalf("prompt is %d bytes; a fat bundle must not crowd out the instructions", len(prompt))
	}
}

func TestRecover_MissingSkillMDIsAnError(t *testing.T) {
	g := fake.New(validSpecJSON(t))
	if _, err := spec.Recover(context.Background(), g, "demo", t.TempDir()); err == nil {
		t.Fatal("Recover accepted a bundle with no SKILL.md")
	}
}
