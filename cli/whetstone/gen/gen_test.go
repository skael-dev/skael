package gen_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skael-dev/skael/cli/whetstone/gen"
	"github.com/skael-dev/skael/internal/eval/lint"
	"github.com/skael-dev/skael/internal/eval/llm/fake"
	"github.com/skael-dev/skael/internal/eval/spec"
)

func genSpec() *spec.SkillSpec {
	return &spec.SkillSpec{
		Name:        "pdf-extract",
		Purpose:     "Extract tables from PDFs.",
		Description: "Extracts tables from PDFs into CSV. Use when the user mentions a PDF or table extraction.",
		Triggers:    []spec.TriggerPhrase{{Text: "extract tables from this pdf"}},
		Steps: []spec.Step{
			{ID: "s1", Action: "Run scripts/extract.py on the input PDF.", Postcondition: "out/tables.csv exists."},
			{ID: "s2", Action: "Run scripts/validate.py out/tables.csv.", Postcondition: "exits 0.", Validation: true},
		},
		Resources:  spec.ResourcePlan{Scripts: []spec.ResourceItem{{Path: "scripts/extract.py", Purpose: "extract"}}},
		TargetTier: spec.TierMid,
	}
}

func TestGenerate_WritesABundle(t *testing.T) {
	out := t.TempDir()
	g := genFake(t, genRoles{})

	b, err := gen.Generate(context.Background(), g, genSpec(), out)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	skillMD := filepath.Join(b.Dir, "SKILL.md")
	content, err := os.ReadFile(skillMD)
	if err != nil {
		t.Fatalf("SKILL.md not written: %v", err)
	}
	if !strings.HasPrefix(string(content), "---\n") {
		t.Errorf("SKILL.md does not start with frontmatter:\n%.200s", content)
	}
	if !strings.Contains(string(content), "name: pdf-extract") {
		t.Error("frontmatter is missing the name")
	}
	if _, err := os.Stat(filepath.Join(b.Dir, "scripts", "extract.py")); err != nil {
		t.Errorf("planned script not written: %v", err)
	}
}

func TestGenerate_UsesTheDescriptionPassNotTheSpecDescription(t *testing.T) {
	out := t.TempDir()
	b, err := gen.Generate(context.Background(), genFake(t, genRoles{}), genSpec(), out)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	content, _ := os.ReadFile(filepath.Join(b.Dir, "SKILL.md"))
	if !strings.Contains(string(content), "a report, or table extraction") {
		t.Errorf("the generated description was not used:\n%.400s", content)
	}
}

func TestGenerate_OutputPassesItsOwnLint(t *testing.T) {
	// The point of the robustness rule set is that generated bundles are clean
	// by construction. If generation can emit a bundle its own linter rejects,
	// the rules are decoration.
	//
	// This asserts zero findings of ANY severity, not just HasErrors(). The
	// warn-tier rules — no-terminal-fallback, description-no-trigger,
	// step-without-postcondition, global-only-guardrail — are exactly the
	// robustness properties this generator exists to produce (a description
	// with no "use when" trigger language is the single most common reason a
	// real skill never fires), so a check that only looks at HasErrors() would
	// let every one of them regress silently. The scripted fixture below
	// currently produces zero findings at any severity, so the tight form is
	// achievable; if a legitimately-generated bundle is ever found to need a
	// warn-tier finding, tighten this to assert the exact expected finding set
	// (by rule name) rather than loosening it back to HasErrors().
	out := t.TempDir()
	b, err := gen.Generate(context.Background(), genFake(t, genRoles{}), genSpec(), out)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	res, err := lint.Run(b.Dir)
	if err != nil {
		t.Fatalf("lint.Run: %v", err)
	}
	for _, f := range res.Findings {
		t.Errorf("generated bundle fails its own lint: [%s] %s %s:%d %s", f.Severity, f.Rule, f.File, f.Line, f.Message)
	}
}

func TestGenerate_BodyPassCarriesTheRobustnessRules(t *testing.T) {
	g := genFake(t, genRoles{})
	if _, err := gen.Generate(context.Background(), g, genSpec(), t.TempDir()); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// genSpec() plans exactly one resource file, so the resources segment is
	// one call: outline, body, 1 resources call, description.
	if calls := g.Calls(); len(calls) != 4 {
		t.Fatalf("made %d gateway calls, want 4 (outline, body, resources, description)", len(calls))
	}
	body := callsByRole(g)["gen.body"].Prompt
	for _, want := range []string{"postcondition", "hedge", "500 lines"} {
		if !strings.Contains(strings.ToLower(body), strings.ToLower(want)) {
			t.Errorf("body prompt does not carry the robustness rule about %q", want)
		}
	}
}

func TestGenerate_RefusesPathsOutsideTheBundle(t *testing.T) {
	// Resource paths come from the approved spec, not the model's response —
	// a traversal planted there must still be refused rather than cleaned.
	s := genSpec()
	s.Resources.Scripts[0].Path = "../../escape.sh"

	out := t.TempDir()
	if _, err := gen.Generate(context.Background(), genFake(t, genRoles{}), s, out); err == nil {
		t.Fatal("Generate accepted a path traversal in a resource path")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(out), "escape.sh")); err == nil {
		t.Error("a file was written outside the bundle directory")
	}
}

func TestGenerate_RefusesAbsolutePaths(t *testing.T) {
	s := genSpec()
	s.Resources.Scripts[0].Path = "/tmp/escape.sh"

	if _, err := gen.Generate(context.Background(), genFake(t, genRoles{}), s, t.TempDir()); err == nil {
		t.Fatal("Generate accepted an absolute resource path")
	}
}

// TestGenerate_OneCallPerPlannedResourceFile is the direct test for the
// resources-pass split: a spec with several planned files must make one
// gateway call per file, tagged by path, with each call's returned content
// landing at its own planned path — not one call asked to emit every file's
// content at once.
func TestGenerate_OneCallPerPlannedResourceFile(t *testing.T) {
	s := genSpec()
	s.Resources = spec.ResourcePlan{
		Scripts: []spec.ResourceItem{
			{Path: "scripts/extract.py", Purpose: "extract"},
			{Path: "scripts/validate.py", Purpose: "validate"},
		},
		References: []spec.ResourceItem{
			{Path: "references/format.md", Purpose: "format notes"},
		},
	}

	// Each planned path answers with its own content, so a mis-mapped file
	// shows up as content landing at the wrong path.
	content := map[string]string{
		"scripts/extract.py":   `{"content":"#!/usr/bin/env python3\nprint(\"extract\")\n"}`,
		"scripts/validate.py":  `{"content":"#!/usr/bin/env python3\nprint(\"validate\")\n"}`,
		"references/format.md": `{"content":"# Format notes\n"}`,
	}
	g := genFake(t, genRoles{Resource: func(path string) string { return content[path] }})

	b, err := gen.Generate(context.Background(), g, s, t.TempDir())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	byRole := callsByRole(g)
	wantRoles := []string{
		"gen.outline", "gen.body",
		"gen.resources:scripts/extract.py", "gen.resources:scripts/validate.py",
		"gen.resources:references/format.md", "gen.description",
	}
	if len(g.Calls()) != len(wantRoles) {
		t.Fatalf("made %d gateway calls, want %d", len(g.Calls()), len(wantRoles))
	}
	for _, want := range wantRoles {
		if _, ok := byRole[want]; !ok {
			t.Errorf("no gateway call with role %q", want)
		}
	}

	for path, want := range map[string]string{
		"scripts/extract.py":   "print(\"extract\")",
		"scripts/validate.py":  "print(\"validate\")",
		"references/format.md": "# Format notes",
	} {
		content, err := os.ReadFile(filepath.Join(b.Dir, filepath.FromSlash(path)))
		if err != nil {
			t.Errorf("planned file %s not written: %v", path, err)
			continue
		}
		if !strings.Contains(string(content), want) {
			t.Errorf("%s content = %q, want it to contain %q", path, content, want)
		}
	}
}

// TestGenerate_NoPlannedResourcesMakesNoResourcesCall pins the other half of
// the split: zero planned files must make zero resources-pass calls, not one
// wasted call asking for an empty file list.
func TestGenerate_NoPlannedResourcesMakesNoResourcesCall(t *testing.T) {
	s := genSpec()
	s.Resources = spec.ResourcePlan{}

	g := genFake(t, genRoles{})

	if _, err := gen.Generate(context.Background(), g, s, t.TempDir()); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	byRole := callsByRole(g)
	if len(g.Calls()) != 3 {
		t.Fatalf("made %d gateway calls, want 3 (outline, body, description; no resources call)", len(g.Calls()))
	}
	for _, want := range []string{"gen.outline", "gen.body", "gen.description"} {
		if _, ok := byRole[want]; !ok {
			t.Errorf("no gateway call with role %q", want)
		}
	}
}

func TestGenerate_PropagatesGatewayFailure(t *testing.T) {
	g := fake.New() // no scripted responses — the first call fails
	if _, err := gen.Generate(context.Background(), g, genSpec(), t.TempDir()); err == nil {
		t.Error("Generate succeeded with a failing gateway")
	}
}

func TestRobustnessRules_CoversTheRuleSet(t *testing.T) {
	rules := strings.ToLower(gen.RobustnessRules())
	for _, want := range []string{
		"postcondition", "numbered", "checkpoint", "guardrail",
		"consider", "500 lines", "template", "stop and report",
	} {
		if !strings.Contains(rules, want) {
			t.Errorf("robustness rules do not mention %q", want)
		}
	}
}
