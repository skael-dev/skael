package spec_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/spec"
)

func fixture() *spec.SkillSpec {
	return &spec.SkillSpec{
		Name:        "pdf-extract",
		Purpose:     "Extract tables from PDF reports into CSV.",
		Description: "Extracts tables from PDF files into CSV. Use when the user mentions a PDF, a report, or table extraction.",
		Triggers: []spec.TriggerPhrase{
			{Text: "pull the tables out of this quarterly PDF"},
			{Text: "summarise this novel", Negative: true},
		},
		Steps: []spec.Step{
			{ID: "s1", Action: "Run scripts/extract.py on the input PDF.", Postcondition: "out/tables.csv exists and is non-empty."},
			{ID: "s2", Action: "Run scripts/validate.py out/tables.csv.", Postcondition: "validate.py exits 0.", Validation: true, Rationale: "malformed CSV silently breaks downstream steps"},
		},
		Constraints: []spec.Rule{
			{ID: "c1", Text: "Never write outside out/.", Kind: spec.RuleMustNot, Severity: spec.SeverityCritical},
		},
		Resources: spec.ResourcePlan{
			Scripts:    []spec.ResourceItem{{Path: "scripts/extract.py", Purpose: "PDF table extraction"}},
			References: []spec.ResourceItem{{Path: "references/pdf-layout.md", Purpose: "layout heuristics"}},
			Assets:     []spec.ResourceItem{{Path: "assets/sample.pdf", Purpose: "fixture PDF for manual testing"}},
		},
		Deps:       spec.DepsDecl{Pip: []string{"pdfplumber"}},
		TargetTier: spec.TierMid,
	}
}

func TestSkillSpec_YAMLRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := fixture().Save(&buf); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := spec.Load(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	want := fixture()
	if got.Name != want.Name || got.Purpose != want.Purpose || got.Description != want.Description {
		t.Errorf("scalar fields lost: %+v", got)
	}
	if len(got.Steps) != 2 || got.Steps[1].Postcondition != want.Steps[1].Postcondition {
		t.Errorf("steps lost: %+v", got.Steps)
	}
	if !got.Steps[1].Validation {
		t.Error("Step.Validation lost in round trip")
	}
	if len(got.Triggers) != 2 || !got.Triggers[1].Negative {
		t.Errorf("negative trigger lost: %+v", got.Triggers)
	}
	if got.Constraints[0].Kind != spec.RuleMustNot || got.Constraints[0].Severity != spec.SeverityCritical {
		t.Errorf("constraint fidelity lost: %+v", got.Constraints[0])
	}
	if len(got.Deps.Pip) != 1 || got.Deps.Pip[0] != "pdfplumber" {
		t.Errorf("deps lost: %+v", got.Deps)
	}
	if got.TargetTier != spec.TierMid {
		t.Errorf("TargetTier = %q, want %q", got.TargetTier, spec.TierMid)
	}
	assertResourcesEqual(t, "YAML", got.Resources, want.Resources)

	// A round trip through the same Go struct cannot by itself catch a wrong
	// yaml tag on ResourceItem: Save and Load both consult the same struct
	// definition, so they would stay symmetric even if "path" were misspelled.
	// Checking the literal rendered text is what actually pins the tag name.
	out := buf.String()
	for _, want := range []string{
		"resources:", "scripts:", "references:", "assets:",
		"path: scripts/extract.py", "path: references/pdf-layout.md", "path: assets/sample.pdf",
		"purpose: PDF table extraction",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered YAML missing %q\n---\n%s", want, out)
		}
	}
}

// assertResourcesEqual checks every field of ResourceItem across all three
// resource kinds, so a wrong tag on ResourceItem cannot lose data silently —
// it is the only place scripts/references/assets fidelity is checked in full.
func assertResourcesEqual(t *testing.T, via string, got, want spec.ResourcePlan) {
	t.Helper()
	check := func(kind string, got, want []spec.ResourceItem) {
		if len(got) != len(want) {
			t.Errorf("%s round trip: resources.%s length = %d, want %d", via, kind, len(got), len(want))
			return
		}
		for i := range want {
			if got[i].Path != want[i].Path || got[i].Purpose != want[i].Purpose {
				t.Errorf("%s round trip: resources.%s[%d] = %+v, want %+v", via, kind, i, got[i], want[i])
			}
		}
	}
	check("scripts", got.Scripts, want.Scripts)
	check("references", got.References, want.References)
	check("assets", got.Assets, want.Assets)
}

func TestSkillSpec_JSONRoundTrip(t *testing.T) {
	// Interview passes SkillSpec through the gateway as JSON (see interview.go),
	// so the json tags need their own round-trip coverage — the YAML round trip
	// above cannot exercise them.
	want := fixture()
	b, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got spec.SkillSpec
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.Name != want.Name || got.Purpose != want.Purpose || got.Description != want.Description {
		t.Errorf("scalar fields lost: %+v", got)
	}
	if len(got.Steps) != 2 || got.Steps[1].Postcondition != want.Steps[1].Postcondition {
		t.Errorf("steps lost: %+v", got.Steps)
	}
	if got.TargetTier != want.TargetTier {
		t.Errorf("TargetTier = %q, want %q", got.TargetTier, want.TargetTier)
	}
	assertResourcesEqual(t, "JSON", got.Resources, want.Resources)

	// As above: json.Marshal/Unmarshal share the same struct, so a wrong json
	// tag round-trips fine in memory. Only the literal wire text pins the name
	// the gateway actually sees — which matters because interview.go's JSON
	// schema (see specSchema in interview.go) names fields explicitly, e.g.
	// "path", and the model is shown that schema, not the Go struct.
	raw := string(b)
	for _, want := range []string{
		`"scripts":[{"path":"scripts/extract.py","purpose":"PDF table extraction"}]`,
		`"references":[{"path":"references/pdf-layout.md","purpose":"layout heuristics"}]`,
		`"assets":[{"path":"assets/sample.pdf","purpose":"fixture PDF for manual testing"}]`,
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("marshaled JSON missing %q\n---\n%s", want, raw)
		}
	}
}

func TestSkillSpec_SaveIsHumanReadable(t *testing.T) {
	// The D4 approval gate shows this YAML to a human. Field names must be
	// snake_case and the output must not be a flow-style one-liner.
	var buf bytes.Buffer
	if err := fixture().Save(&buf); err != nil {
		t.Fatalf("Save: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"name: pdf-extract", "postcondition:", "target_tier:", "must_not"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered spec missing %q\n---\n%s", want, out)
		}
	}
	if strings.Count(out, "\n") < 15 {
		t.Errorf("spec rendered too densely to review:\n%s", out)
	}
}

func TestSkillSpec_Validate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*spec.SkillSpec)
		wantErr string
	}{
		{"valid", func(*spec.SkillSpec) {}, ""},
		{"no name", func(s *spec.SkillSpec) { s.Name = "" }, "name is required"},
		{"name too long", func(s *spec.SkillSpec) { s.Name = strings.Repeat("a", 65) }, "64"},
		{"name not kebab", func(s *spec.SkillSpec) { s.Name = "PDF_Extract" }, "lowercase"},
		{"no steps", func(s *spec.SkillSpec) { s.Steps = nil }, "at least one step"},
		{"duplicate step id", func(s *spec.SkillSpec) { s.Steps[1].ID = "s1" }, "duplicate step id"},
		{"step without postcondition", func(s *spec.SkillSpec) { s.Steps[0].Postcondition = "" }, "postcondition"},
		{"description too long", func(s *spec.SkillSpec) { s.Description = strings.Repeat("x", 1025) }, "1024"},
		{"no positive trigger", func(s *spec.SkillSpec) { s.Triggers = []spec.TriggerPhrase{{Text: "no", Negative: true}} }, "positive trigger"},
		{"too many modules", func(s *spec.SkillSpec) {
			s.Resources.Scripts = []spec.ResourceItem{{Path: "a"}, {Path: "b"}}
			s.Resources.References = []spec.ResourceItem{{Path: "c"}, {Path: "d"}}
		}, "at most 3"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := fixture()
			tt.mutate(s)
			errs := s.Validate()
			if tt.wantErr == "" {
				if len(errs) != 0 {
					t.Fatalf("Validate() = %v, want no errors", errs)
				}
				return
			}
			var joined string
			for _, e := range errs {
				joined += e.Error() + "\n"
			}
			if !strings.Contains(joined, tt.wantErr) {
				t.Errorf("Validate() = %q, want an error containing %q", joined, tt.wantErr)
			}
		})
	}
}

func TestSkillSpec_DirName(t *testing.T) {
	// A namespaced registry name must still yield a spec-legal directory (P7).
	for in, want := range map[string]string{
		"pdf-extract":               "pdf-extract",
		"superpowers:brainstorming": "brainstorming",
		"a:b:c":                     "c",
	} {
		s := &spec.SkillSpec{Name: in}
		if got := s.DirName(); got != want {
			t.Errorf("DirName(%q) = %q, want %q", in, got, want)
		}
	}
}
