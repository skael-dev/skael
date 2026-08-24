package gen_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/skael-dev/skael/cli/whetstone/gen"
	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/eval/spec"
)

// TestGenerate_PassesAndRoles asserts every pass runs exactly once, tagged
// with the matching Role, and that only the body pass carries the robustness
// rules. Order is no longer asserted beyond the one real dependency: the body
// pass follows the outline, while the resources and description passes run
// beside that chain, so a fixed call sequence would only pin the scheduler.
func TestGenerate_PassesAndRoles(t *testing.T) {
	g := genFake(t, genRoles{})
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

	byRole := callsByRole(g)
	wantRoles := []string{"gen.outline", "gen.body", "gen.description"}
	for _, items := range [][]spec.ResourceItem{s.Resources.Scripts, s.Resources.References, s.Resources.Assets} {
		for _, it := range items {
			wantRoles = append(wantRoles, "gen.resources:"+it.Path)
		}
	}
	for _, role := range wantRoles {
		if _, ok := byRole[role]; !ok {
			t.Errorf("no gateway call with role %q", role)
		}
	}

	// The one order that is a data dependency.
	outlineAt, bodyAt := -1, -1
	for i, c := range calls {
		switch c.Role {
		case "gen.outline":
			outlineAt = i
		case "gen.body":
			bodyAt = i
		}
	}
	if outlineAt > bodyAt {
		t.Error("the body pass ran before the outline pass it reads")
	}

	bodyPrompt := strings.ToLower(byRole["gen.body"].Prompt)
	if !strings.Contains(bodyPrompt, "postcondition") || !strings.Contains(bodyPrompt, "500 lines") {
		t.Error("body pass prompt does not carry the robustness rules")
	}

	descPrompt := strings.ToLower(byRole["gen.description"].Prompt)
	if strings.Contains(descPrompt, "hedge") || strings.Contains(descPrompt, "500 lines") {
		t.Error("description pass prompt unexpectedly carries body robustness-rule language")
	}
}

// TestGenerate_OutlineAsksForTheFastClass pins the cheaper slot for the pass
// that returns a list of headings. With a single-entry LLM_MODEL both slots
// resolve to one model, so this costs that setup nothing.
func TestGenerate_OutlineAsksForTheFastClass(t *testing.T) {
	g := genFake(t, genRoles{})
	if _, err := gen.Generate(context.Background(), g, genSpec(), t.TempDir()); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if got := callsByRole(g)["gen.outline"].ModelClass; got != llm.ClassFast {
		t.Errorf("gen.outline asked for class %q, want %q", got, llm.ClassFast)
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
			if _, err := gen.Generate(context.Background(), genFake(t, genRoles{}), s, out); err == nil {
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

	g := genFake(t, genRoles{})
	if _, err := gen.Generate(context.Background(), g, s, t.TempDir()); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	body := callsByRole(g)["gen.body"].Prompt

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

// TestGenerate_ResourceFilesKeepPlanOrderWhateverTheCompletionOrder guards the
// concurrent resources pass: the first planned file answers last, so an
// implementation that appended on completion would write each file's content
// at another file's path.
func TestGenerate_ResourceFilesKeepPlanOrderWhateverTheCompletionOrder(t *testing.T) {
	s := genSpec()
	s.Resources = spec.ResourcePlan{
		Scripts: []spec.ResourceItem{
			{Path: "scripts/first.py", Purpose: "first"},
			{Path: "scripts/second.py", Purpose: "second"},
		},
	}

	g := genFake(t, genRoles{Resource: func(path string) string {
		if path == "scripts/first.py" {
			time.Sleep(50 * time.Millisecond)
		}
		return `{"content":"# ` + path + `\n"}`
	}})

	b, err := gen.Generate(context.Background(), g, s, t.TempDir())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, path := range []string{"scripts/first.py", "scripts/second.py"} {
		content, err := os.ReadFile(filepath.Join(b.Dir, filepath.FromSlash(path)))
		if err != nil {
			t.Errorf("planned file %s not written: %v", path, err)
			continue
		}
		if !strings.Contains(string(content), path) {
			t.Errorf("%s holds %q, want its own content — the pass kept completion order", path, content)
		}
	}
}

// TestGenerate_AFailedResourceCallCancelsTheOthers pins the failure path of
// the concurrent pass: one error ends the generation, and names that file.
func TestGenerate_AFailedResourceCallCancelsTheOthers(t *testing.T) {
	s := genSpec()
	s.Resources = spec.ResourcePlan{
		Scripts: []spec.ResourceItem{
			{Path: "scripts/broken.py"},
			{Path: "scripts/slow.py"},
		},
	}

	g := genFake(t, genRoles{Resource: func(path string) string {
		if path == "scripts/broken.py" {
			return "not json at all"
		}
		time.Sleep(50 * time.Millisecond)
		return `{"content":"ok\n"}`
	}})

	if _, err := gen.Generate(context.Background(), g, s, t.TempDir()); err == nil {
		t.Fatal("Generate succeeded with a failing resource call")
	} else if !strings.Contains(err.Error(), "broken.py") {
		t.Errorf("error = %q, want the failing file named", err)
	}
}
