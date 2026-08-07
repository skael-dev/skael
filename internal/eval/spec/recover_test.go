package spec_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/eval/spec"
)

// fakeGateway records requests and returns scripted responses.
type fakeGateway struct {
	reply      string
	replies    []string
	calls      int
	lastPrompt string
}

func (g *fakeGateway) Complete(_ context.Context, r llm.Req) (llm.Res, error) {
	g.calls++
	g.lastPrompt = r.Prompt

	var resp string
	if len(g.replies) > 0 {
		if g.calls <= len(g.replies) {
			resp = g.replies[g.calls-1]
		}
	} else {
		resp = g.reply
	}

	return llm.Res{Text: resp, Model: "fake"}, nil
}

func (g *fakeGateway) ModelFor(_ llm.ModelClass) string {
	return "fake"
}

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

	g := &fakeGateway{reply: validSpecJSON(t)}
	if _, err := spec.Recover(context.Background(), g, "pdf-split", dir); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	// The whole point of reading the bundle rather than just SKILL.md is that
	// a skill's real behaviour often lives in its scripts.
	if !strings.Contains(g.lastPrompt, "qpdf --split-pages") {
		t.Fatal("prompt does not include scripts/ content")
	}
	if !strings.Contains(g.lastPrompt, "Run scripts/split.sh.") {
		t.Fatal("prompt does not include the SKILL.md body")
	}
}

func TestRecover_NamesTheSkillFromTheRegistry(t *testing.T) {
	// A namespaced registry name must survive: Validate checks DirName(),
	// which strips everything up to the last colon, so "superpowers:brainstorming"
	// is legal even though the raw name is not kebab-case.
	dir := newMinimalBundle(t)
	g := &fakeGateway{reply: validSpecJSONNamed(t, "something-else")}

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
	g := &fakeGateway{replies: []string{invalidSpecJSON(t), validSpecJSON(t)}}

	if _, err := spec.Recover(context.Background(), g, "demo", dir); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if g.calls != 2 {
		t.Fatalf("gateway called %d times, want 2 (draft + repair)", g.calls)
	}
	if !strings.Contains(g.lastPrompt, "postcondition") {
		t.Fatal("repair prompt does not name the validation failure")
	}
}

func TestRecover_ValidDraftCostsOneCall(t *testing.T) {
	// Interview's unconditional second call is a design critique. Recovering
	// an existing skill has no design to improve, so it is not paid for.
	dir := newMinimalBundle(t)
	g := &fakeGateway{reply: validSpecJSON(t)}

	if _, err := spec.Recover(context.Background(), g, "demo", dir); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if g.calls != 1 {
		t.Fatalf("gateway called %d times for a valid draft, want 1", g.calls)
	}
}

func TestRecover_TruncatesAFatBundle(t *testing.T) {
	dir := newMinimalBundle(t)
	writeFile(t, dir, "references/huge.md", strings.Repeat("x", 200_000))

	g := &fakeGateway{reply: validSpecJSON(t)}
	if _, err := spec.Recover(context.Background(), g, "demo", dir); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if len(g.lastPrompt) > 80_000 {
		t.Fatalf("prompt is %d bytes; a fat bundle must not crowd out the instructions", len(g.lastPrompt))
	}
}

func TestRecover_MissingSkillMDIsAnError(t *testing.T) {
	g := &fakeGateway{reply: validSpecJSON(t)}
	if _, err := spec.Recover(context.Background(), g, "demo", t.TempDir()); err == nil {
		t.Fatal("Recover accepted a bundle with no SKILL.md")
	}
}
