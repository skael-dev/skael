package eval_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/skael-dev/skael/internal/eval/lint"
	"github.com/skael-dev/skael/internal/eval/spec"
	"github.com/skael-dev/skael/internal/eval/suite"
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

// Every archetype's spec must be valid, and must carry the trigger phrases
// evals/triggers.json is derived from — a spec with none produces an eval set
// that cannot measure firing at all.
func TestCorpus_SpecsAreValidAndCarryTriggers(t *testing.T) {
	for _, name := range archetypes(t) {
		t.Run(name, func(t *testing.T) {
			sp := loadSpec(t, filepath.Join("testdata", "corpus", name, "spec.yaml"))
			if errs := sp.Validate(); len(errs) > 0 {
				t.Fatalf("spec is not valid: %v", errs)
			}
			if len(suite.TriggersFromSpec(sp)) == 0 {
				t.Error("spec carries no trigger phrases; the derived trigger set would be empty")
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

// TestCorpus_ExercisesTheBrokenLinkRule proves the corpus actually feeds the
// broken-link rule an input. A corpus whose SKILL.md files contain no relative
// markdown link at all would pass every expectation above while leaving the
// rule entirely unexercised — so this copies an archetype, removes the file its
// link resolves to, and requires the finding to appear.
func TestCorpus_ExercisesTheBrokenLinkRule(t *testing.T) {
	src := filepath.Join("testdata", "corpus", "document-formatter")
	dst := filepath.Join(t.TempDir(), "document-formatter")
	copyTree(t, src, dst)

	res, err := lint.Run(dst)
	if err != nil {
		t.Fatalf("lint.Run: %v", err)
	}
	if hasRule(res, "broken-link") {
		t.Fatalf("the unmodified corpus already reports a broken link: %+v", res.Findings)
	}

	if err := os.Remove(filepath.Join(dst, "references", "style-guide.md")); err != nil {
		t.Fatal(err)
	}

	res, err = lint.Run(dst)
	if err != nil {
		t.Fatalf("lint.Run: %v", err)
	}
	if !hasRule(res, "broken-link") {
		t.Errorf("removing a linked corpus file produced no broken-link finding: %+v", res.Findings)
	}
}

func hasRule(res *lint.Result, rule string) bool {
	for _, f := range res.Findings {
		if f.Rule == rule {
			return true
		}
	}
	return false
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// loadSpec reads an archetype's authored spec.
func loadSpec(t *testing.T, path string) *spec.SkillSpec {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open spec: %v", err)
	}
	defer func() { _ = f.Close() }()
	sp, err := spec.Load(f)
	if err != nil {
		t.Fatalf("parse spec: %v", err)
	}
	return sp
}
