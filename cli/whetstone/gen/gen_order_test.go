package gen_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skael-dev/skael/cli/whetstone/gen"
	"github.com/skael-dev/skael/internal/eval/llm/fake"
	"github.com/skael-dev/skael/internal/eval/spec"
)

// TestGenerate_PassOrderAndRoles asserts the gateway calls happen in the
// documented order — outline, body, one call per planned resource file,
// description — tagged with the matching Role, and that only the body pass
// carries the robustness rules. A regression that swapped two passes (e.g.
// resources before body) would still make the same number of calls and could
// still pass TestGenerate_WritesABundle against this scripted fixture, so
// call count alone does not catch it; the Role sequence does. The resources
// segment is asserted by shape (one "gen.resources:<path>" role per planned
// path, in Scripts→References→Assets order) rather than a fixed length, since
// that segment's length is what the split made variable.
func TestGenerate_PassOrderAndRoles(t *testing.T) {
	g := fake.New(scripted()...)
	s := genSpec()
	if _, err := gen.Generate(context.Background(), g, s, t.TempDir()); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	calls := g.Calls()
	wantLen := 2 + s.Resources.Count() + 1
	if len(calls) != wantLen {
		t.Fatalf("made %d gateway calls, want %d (outline, body, %d resources, description)",
			len(calls), wantLen, s.Resources.Count())
	}

	if calls[0].Role != "gen.outline" {
		t.Errorf("call 0 has role %q, want %q", calls[0].Role, "gen.outline")
	}
	if calls[1].Role != "gen.body" {
		t.Errorf("call 1 has role %q, want %q", calls[1].Role, "gen.body")
	}

	var wantPaths []string
	for _, items := range [][]spec.ResourceItem{s.Resources.Scripts, s.Resources.References, s.Resources.Assets} {
		for _, it := range items {
			wantPaths = append(wantPaths, it.Path)
		}
	}
	for i, path := range wantPaths {
		call := calls[2+i]
		want := "gen.resources:" + path
		if call.Role != want {
			t.Errorf("call %d has role %q, want %q (resources segment must follow Scripts→References→Assets order)",
				2+i, call.Role, want)
		}
	}

	last := calls[len(calls)-1]
	if last.Role != "gen.description" {
		t.Errorf("last call has role %q, want %q", last.Role, "gen.description")
	}

	bodyPrompt := strings.ToLower(calls[1].Prompt)
	if !strings.Contains(bodyPrompt, "postcondition") || !strings.Contains(bodyPrompt, "500 lines") {
		t.Error("body pass prompt does not carry the robustness rules")
	}

	descPrompt := strings.ToLower(last.Prompt)
	if strings.Contains(descPrompt, "hedge") || strings.Contains(descPrompt, "500 lines") {
		t.Error("description pass prompt unexpectedly carries body robustness-rule language")
	}
}

// TestGenerate_RefusesNestedTraversalResourcePath proves a traversal buried
// mid-path — not just a leading ".." — is also refused. A check that only
// inspects a path's prefix (e.g. strings.HasPrefix(rel, "..")) would miss
// both of these; see TestNaivePrefixCheck_WouldMissNestedTraversal in
// bundlepath_test.go for the direct demonstration. The evil path is planted
// in the spec's resource plan, not the model's response: that is where a
// resource path comes from now, and assemble must refuse it regardless.
func TestGenerate_RefusesNestedTraversalResourcePath(t *testing.T) {
	for _, evil := range []string{"scripts/../../escape.sh", "./../escape.sh"} {
		t.Run(evil, func(t *testing.T) {
			s := genSpec()
			s.Resources.Scripts[0].Path = evil

			out := t.TempDir()
			if _, err := gen.Generate(context.Background(), fake.New(scripted()...), s, out); err == nil {
				t.Fatalf("Generate accepted nested-traversal resource path %q", evil)
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(out), "escape.sh")); err == nil {
				t.Error("a file was written outside the bundle directory")
			}
		})
	}
}

// TestGenerate_BodyPromptCarriesTheConstraints closes the gap between what the
// generator is told and what the drift contract scores. contract.Compile turns
// every MUST-NOT into a violation weighted by its severity, and the task
// prompts render them — but the body pass never saw them, so the generated
// SKILL.md was written without knowing the rules it would be penalized for
// breaking. The guardrail placement lint checks for could only be satisfied by
// accident.
func TestGenerate_BodyPromptCarriesTheConstraints(t *testing.T) {
	s := genSpec()
	s.Constraints = []spec.Rule{
		{ID: "c1", Text: "Never write outside out/.", Kind: spec.RuleMustNot, Severity: spec.SeverityCritical},
		{ID: "c2", Text: "Always validate the CSV before reporting success.", Kind: spec.RuleMust, Severity: spec.SeverityMajor},
	}

	g := fake.New(scripted()...)
	if _, err := gen.Generate(context.Background(), g, s, t.TempDir()); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	calls := g.Calls()
	if len(calls) != 4 {
		t.Fatalf("made %d gateway calls, want 4", len(calls))
	}
	body := calls[1].Prompt

	for _, want := range []string{
		"Never write outside out/.",
		"Always validate the CSV before reporting success.",
		"MUST NOT",
		"MUST",
		"critical",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body pass prompt does not carry %q:\n%s", want, body)
		}
	}
}
