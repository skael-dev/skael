package eval_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/skael-dev/skael/internal/eval/contract"
	"github.com/skael-dev/skael/internal/eval/lint"
	"github.com/skael-dev/skael/internal/eval/spec"
)

type expectedFinding struct {
	Rule     string `json:"rule"`
	Severity string `json:"severity"`
	File     string `json:"file"`
}

func archetypes(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join("testdata", "corpus"))
	if err != nil {
		t.Fatalf("read corpus: %v", err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	if len(out) != 3 {
		t.Fatalf("corpus has %d archetypes, want 3", len(out))
	}
	return out
}

func TestCorpus_LintOutputIsStable(t *testing.T) {
	for _, name := range archetypes(t) {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join("testdata", "corpus", name)

			res, err := lint.Run(dir)
			if err != nil {
				t.Fatalf("lint.Run: %v", err)
			}

			got := make([]expectedFinding, 0, len(res.Findings))
			for _, f := range res.Findings {
				got = append(got, expectedFinding{Rule: f.Rule, Severity: string(f.Severity), File: f.File})
			}
			sortFindings(got)

			raw, err := os.ReadFile(filepath.Join(dir, "expected-lint.json"))
			if err != nil {
				t.Fatalf("read expectation: %v", err)
			}
			var want []expectedFinding
			if err := json.Unmarshal(raw, &want); err != nil {
				t.Fatalf("parse expectation: %v", err)
			}
			sortFindings(want)

			if len(got) != len(want) {
				t.Fatalf("lint produced %d findings, expected %d\ngot:  %+v\nwant: %+v", len(got), len(want), got, want)
			}
			for i := range got {
				if got[i] != want[i] {
					t.Errorf("finding %d = %+v, want %+v", i, got[i], want[i])
				}
			}
		})
	}
}

func TestCorpus_SpecsAreValidAndCompile(t *testing.T) {
	for _, name := range archetypes(t) {
		t.Run(name, func(t *testing.T) {
			f, err := os.Open(filepath.Join("testdata", "corpus", name, "spec.yaml"))
			if err != nil {
				t.Fatalf("open spec: %v", err)
			}
			defer f.Close()

			sp, err := spec.Load(f)
			if err != nil {
				t.Fatalf("spec.Load: %v", err)
			}
			if errs := sp.Validate(); len(errs) != 0 {
				t.Fatalf("corpus spec is invalid: %v", errs)
			}

			c, err := contract.Compile(sp)
			if err != nil {
				t.Fatalf("contract.Compile: %v", err)
			}
			// Every archetype must yield something checkable, or the contract
			// carries no deterministic signal at all.
			if len(c.Steps) == 0 && len(c.Forbid) == 0 {
				t.Errorf("contract has no deterministic matchers: %+v", c)
			}

			// checkpointed-workflow specifically must yield BOTH kinds: it
			// exists to pin that a MUST-NOT constraint compiles to a forbid
			// rule, not just that the spec's steps produce step matchers. The
			// generic disjunction above would still pass if classifyForbid
			// silently stopped firing and the constraint was demoted to a
			// SemanticRule instead.
			if name == "checkpointed-workflow" {
				if len(c.Steps) == 0 {
					t.Errorf("no step matchers: %+v", c)
				}
				if len(c.Forbid) == 0 {
					t.Errorf("MUST-NOT constraint did not compile to a forbid rule: %+v", c)
				}
			}
		})
	}
}

func TestCorpus_OneArchetypeIsCleanAndOneIsNot(t *testing.T) {
	// The corpus is only a regression net if it exercises both outcomes. Three
	// bundles that all lint clean would not catch a linter that stopped firing.
	var clean, dirty int
	for _, name := range archetypes(t) {
		res, err := lint.Run(filepath.Join("testdata", "corpus", name))
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Findings) == 0 {
			clean++
		} else {
			dirty++
		}
	}
	if clean == 0 {
		t.Error("no archetype lints completely clean")
	}
	if dirty == 0 {
		t.Error("no archetype produces findings; a silent linter would pass this corpus")
	}
}

func sortFindings(f []expectedFinding) {
	sort.Slice(f, func(i, j int) bool {
		if f[i].File != f[j].File {
			return f[i].File < f[j].File
		}
		if f[i].Rule != f[j].Rule {
			return f[i].Rule < f[j].Rule
		}
		return f[i].Severity < f[j].Severity
	})
}
