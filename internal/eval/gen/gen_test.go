package gen_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/gen"
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

// scripted returns the four gateway responses a full generation needs.
func scripted() []string {
	return []string{
		`{"sections":["Overview","Steps","Failure handling"]}`,
		`{"body":"# PDF Extract\n\n1. Run ` + "`scripts/extract.py <input.pdf>`" + `. Postcondition: out/tables.csv exists.\n2. Run ` + "`scripts/validate.py out/tables.csv`" + `. Postcondition: exits 0.\n\nIf a checkpoint cannot be satisfied after one retry, stop and report state.\n"}`,
		`{"files":[{"path":"scripts/extract.py","content":"#!/usr/bin/env python3\n\"\"\"Extract tables.\"\"\"\nimport sys\nif \"--help\" in sys.argv:\n    print(__doc__); sys.exit(0)\n"}]}`,
		`{"description":"Extracts tables from PDF files into CSV. Use when the user mentions a PDF, a report, or table extraction."}`,
	}
}

func TestGenerate_WritesABundle(t *testing.T) {
	out := t.TempDir()
	g := fake.New(scripted()...)

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
	b, err := gen.Generate(context.Background(), fake.New(scripted()...), genSpec(), out)
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
	out := t.TempDir()
	b, err := gen.Generate(context.Background(), fake.New(scripted()...), genSpec(), out)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	res, err := lint.Run(b.Dir)
	if err != nil {
		t.Fatalf("lint.Run: %v", err)
	}
	if res.HasErrors() {
		for _, f := range res.Findings {
			if f.Severity == lint.SeverityError {
				t.Errorf("generated bundle fails its own lint: %s %s:%d %s", f.Rule, f.File, f.Line, f.Message)
			}
		}
	}
}

func TestGenerate_BodyPassCarriesTheRobustnessRules(t *testing.T) {
	g := fake.New(scripted()...)
	if _, err := gen.Generate(context.Background(), g, genSpec(), t.TempDir()); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	calls := g.Calls()
	if len(calls) != 4 {
		t.Fatalf("made %d gateway calls, want 4 (outline, body, resources, description)", len(calls))
	}
	body := calls[1].Prompt
	for _, want := range []string{"postcondition", "hedge", "500 lines"} {
		if !strings.Contains(strings.ToLower(body), strings.ToLower(want)) {
			t.Errorf("body prompt does not carry the robustness rule about %q", want)
		}
	}
}

func TestGenerate_RefusesPathsOutsideTheBundle(t *testing.T) {
	// The resources pass returns model-authored paths. A traversal would write
	// outside the bundle directory, so it must be refused rather than cleaned.
	responses := scripted()
	responses[2] = `{"files":[{"path":"../../escape.sh","content":"#!/bin/sh\n"}]}`

	out := t.TempDir()
	if _, err := gen.Generate(context.Background(), fake.New(responses...), genSpec(), out); err == nil {
		t.Fatal("Generate accepted a path traversal in a resource path")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(out), "escape.sh")); err == nil {
		t.Error("a file was written outside the bundle directory")
	}
}

func TestGenerate_RefusesAbsolutePaths(t *testing.T) {
	responses := scripted()
	responses[2] = `{"files":[{"path":"/tmp/escape.sh","content":"x"}]}`

	if _, err := gen.Generate(context.Background(), fake.New(responses...), genSpec(), t.TempDir()); err == nil {
		t.Fatal("Generate accepted an absolute resource path")
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
