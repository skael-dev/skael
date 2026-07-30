package contract_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/contract"
	"github.com/skael-dev/skael/internal/eval/spec"
	"github.com/skael-dev/skael/internal/eval/trajectory"
)

func testSpec() *spec.SkillSpec {
	return &spec.SkillSpec{
		Name:        "pdf-extract",
		Purpose:     "Extract tables.",
		Description: "Extracts tables. Use when a PDF is mentioned.",
		Triggers:    []spec.TriggerPhrase{{Text: "extract tables"}},
		Steps: []spec.Step{
			{ID: "s1", Action: "Run scripts/extract.py on the input.", Postcondition: "out/tables.csv exists."},
			{ID: "s2", Action: "Run scripts/validate.py out/tables.csv.", Postcondition: "exits 0.", Validation: true},
			{ID: "s3", Action: "Summarise the result for the user in a formal tone.", Postcondition: "a summary is produced."},
		},
		Constraints: []spec.Rule{
			{ID: "c1", Text: "Never write outside out/.", Kind: spec.RuleMustNot, Severity: spec.SeverityCritical},
			{ID: "c2", Text: "Keep the report tone formal.", Kind: spec.RuleMust, Severity: spec.SeverityMinor},
		},
		TargetTier: spec.TierMid,
	}
}

func TestCompile_ScriptStepsBecomeShellMatchers(t *testing.T) {
	c, err := contract.Compile(testSpec())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	var s1 *contract.StepMatch
	for i := range c.Steps {
		if c.Steps[i].ID == "s1" {
			s1 = &c.Steps[i]
		}
	}
	if s1 == nil {
		t.Fatalf("no matcher compiled for s1: %+v", c.Steps)
	}
	if s1.Match.Type != trajectory.TypeShell {
		t.Errorf("s1 match type = %q, want shell", s1.Match.Type)
	}
	if s1.Match.Pattern == "" {
		t.Error("s1 has no pattern; a shell matcher with no pattern matches every command")
	}
	if !s1.Required {
		t.Error("s1 should be required")
	}
}

func TestCompile_PatternEscapesRegexMetacharacters(t *testing.T) {
	// "scripts/extract.py" contains a '.', which unescaped matches any
	// character — so the matcher would also accept "scripts/extractXpy".
	c, _ := contract.Compile(testSpec())
	for _, s := range c.Steps {
		if s.ID == "s1" && !bytes.Contains([]byte(s.Match.Pattern), []byte(`\.`)) {
			t.Errorf("s1 pattern %q does not escape the dot in the script name", s.Match.Pattern)
		}
	}
}

func TestCompile_OrderingIsDerivedFromStepSequence(t *testing.T) {
	c, _ := contract.Compile(testSpec())
	for _, s := range c.Steps {
		if s.ID == "s2" {
			if len(s.Order.After) == 0 || s.Order.After[0] != "s1" {
				t.Errorf("s2 order = %+v, want after s1", s.Order)
			}
		}
	}
}

func TestCompile_ValidationStepsBecomeCheckpoints(t *testing.T) {
	c, _ := contract.Compile(testSpec())
	if len(c.Checkpoints) != 1 || c.Checkpoints[0] != "s2" {
		t.Errorf("Checkpoints = %v, want [s2]", c.Checkpoints)
	}
}

func TestCompile_MustNotConstraintsBecomeForbidRules(t *testing.T) {
	c, _ := contract.Compile(testSpec())

	if len(c.Forbid) == 0 {
		t.Fatal("no forbid rules compiled from a MUST-NOT constraint")
	}
	f := c.Forbid[0]
	if f.Severity != spec.SeverityCritical {
		t.Errorf("forbid severity = %q, want critical", f.Severity)
	}
	if f.Match.Type != trajectory.TypeFileWrite {
		t.Errorf("forbid match type = %q, want file_write", f.Match.Type)
	}
	if f.Match.PathNotGlob == "" {
		t.Errorf("forbid rule has no path constraint: %+v", f.Match)
	}
}

func TestCompile_UnmatchableStepsAreDemotedToSemantic(t *testing.T) {
	// "Summarise the result in a formal tone" cannot be checked against any
	// normalized event. It must become a semantic rule rather than a step
	// matcher that can never be satisfied — a matcher nothing can satisfy
	// scores every run as a step failure.
	c, _ := contract.Compile(testSpec())

	for _, s := range c.Steps {
		if s.ID == "s3" {
			t.Errorf("unmatchable step s3 compiled to a step matcher: %+v", s)
		}
	}
	var found bool
	for _, sem := range c.Semantic {
		if sem.ID == "s3" {
			found = true
		}
	}
	if !found {
		t.Errorf("s3 was not demoted to a semantic rule: %+v", c.Semantic)
	}
}

func TestCompile_UnverifiableConstraintsBecomeSemantic(t *testing.T) {
	// "Keep the report tone formal" is a MUST with no observable event.
	c, _ := contract.Compile(testSpec())
	for _, f := range c.Forbid {
		if f.ID == "c2" {
			t.Errorf("tone constraint compiled to a forbid rule: %+v", f)
		}
	}
	var found bool
	for _, sem := range c.Semantic {
		if sem.ID == "c2" {
			found = true
		}
	}
	if !found {
		t.Errorf("c2 not demoted to semantic: %+v", c.Semantic)
	}
}

func TestCompile_EveryMatcherIsCheckable(t *testing.T) {
	// The invariant the whole compiler exists to guarantee.
	c, _ := contract.Compile(testSpec())

	checkable := map[trajectory.EventType]bool{
		trajectory.TypeShell: true, trajectory.TypeFileRead: true,
		trajectory.TypeFileWrite: true, trajectory.TypeToolCall: true,
		trajectory.TypeSkillRead: true, trajectory.TypeAskUser: true,
	}
	for _, s := range c.Steps {
		if !checkable[s.Match.Type] {
			t.Errorf("step %s has an uncheckable match type %q", s.ID, s.Match.Type)
		}
	}
	for _, f := range c.Forbid {
		if !checkable[f.Match.Type] {
			t.Errorf("forbid %s has an uncheckable match type %q", f.ID, f.Match.Type)
		}
	}
}

func TestContract_YAMLRoundTrip(t *testing.T) {
	want, _ := contract.Compile(testSpec())

	var buf bytes.Buffer
	if err := want.Save(&buf); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := contract.Load(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got.Version != want.Version {
		t.Errorf("Version = %d, want %d", got.Version, want.Version)
	}
	if len(got.Steps) != len(want.Steps) || len(got.Forbid) != len(want.Forbid) ||
		len(got.Semantic) != len(want.Semantic) || len(got.Checkpoints) != len(want.Checkpoints) {
		t.Errorf("round trip lost content: %+v", got)
	}
	if len(got.Forbid) > 0 && got.Forbid[0].Severity != want.Forbid[0].Severity {
		t.Error("forbid severity lost in round trip")
	}
}

// TestContract_YAMLRoundTrip_AllForbidSeverities extends the round-trip check
// beyond the first forbid rule: severity weights ViolationScore later, so a
// wrong or dropped severity mis-scores a run instead of erroring loudly. This
// also asserts Order and Checkpoints survive intact, and is written so that
// misspelling a yaml tag on any of those fields turns it red (proven in the
// implementation report, not re-verified by CI).
func TestContract_YAMLRoundTrip_SeverityOrderCheckpoints(t *testing.T) {
	s := testSpec()
	// Add a second forbid-worthy constraint with a different severity so a
	// single dropped/mis-mapped severity can't hide behind a match on c1.
	s.Constraints = append(s.Constraints, spec.Rule{
		ID:       "c3",
		Text:     "Must not connect to the network.",
		Kind:     spec.RuleMustNot,
		Severity: spec.SeverityMajor,
	})

	want, err := contract.Compile(s)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if len(want.Forbid) < 2 {
		t.Fatalf("test setup: expected at least 2 forbid rules, got %+v", want.Forbid)
	}

	var buf bytes.Buffer
	if err := want.Save(&buf); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := contract.Load(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(got.Forbid) != len(want.Forbid) {
		t.Fatalf("Forbid length = %d, want %d", len(got.Forbid), len(want.Forbid))
	}
	for i := range want.Forbid {
		if got.Forbid[i].ID != want.Forbid[i].ID {
			t.Errorf("Forbid[%d].ID = %q, want %q", i, got.Forbid[i].ID, want.Forbid[i].ID)
		}
		if got.Forbid[i].Severity != want.Forbid[i].Severity {
			t.Errorf("Forbid[%d] (%s) severity = %q, want %q", i, want.Forbid[i].ID, got.Forbid[i].Severity, want.Forbid[i].Severity)
		}
	}

	if len(got.Steps) != len(want.Steps) {
		t.Fatalf("Steps length = %d, want %d", len(got.Steps), len(want.Steps))
	}
	for i := range want.Steps {
		if got.Steps[i].Order.Mode != want.Steps[i].Order.Mode {
			t.Errorf("Steps[%d] (%s) Order.Mode = %q, want %q", i, want.Steps[i].ID, got.Steps[i].Order.Mode, want.Steps[i].Order.Mode)
		}
		if len(got.Steps[i].Order.After) != len(want.Steps[i].Order.After) {
			t.Errorf("Steps[%d] (%s) Order.After = %v, want %v", i, want.Steps[i].ID, got.Steps[i].Order.After, want.Steps[i].Order.After)
			continue
		}
		for j := range want.Steps[i].Order.After {
			if got.Steps[i].Order.After[j] != want.Steps[i].Order.After[j] {
				t.Errorf("Steps[%d] (%s) Order.After[%d] = %q, want %q", i, want.Steps[i].ID, j, got.Steps[i].Order.After[j], want.Steps[i].Order.After[j])
			}
		}
	}

	if len(got.Checkpoints) != len(want.Checkpoints) {
		t.Fatalf("Checkpoints = %v, want %v", got.Checkpoints, want.Checkpoints)
	}
	for i := range want.Checkpoints {
		if got.Checkpoints[i] != want.Checkpoints[i] {
			t.Errorf("Checkpoints[%d] = %q, want %q", i, got.Checkpoints[i], want.Checkpoints[i])
		}
	}

	// Round-tripping through the same struct is tautological about the wire
	// format: a struct whose Severity/Order/Checkpoints tags were all
	// renamed identically on encode and decode would still "round-trip"
	// successfully. Assert the raw YAML actually uses the documented,
	// external-facing key names, so a misspelled tag is caught even though
	// Save and Load agree with each other.
	raw := buf.String()
	for _, key := range []string{"severity:", "mode:", "after:", "checkpoints:"} {
		if !strings.Contains(raw, key) {
			t.Errorf("compiled contract YAML missing expected key %q:\n%s", key, raw)
		}
	}
}

// TestContract_IDsArePreservedAndUnique guards a failure mode that would
// otherwise be silent: if two emitted items of the same kind collided on ID,
// a later report would attribute a violation to the wrong rule rather than
// erroring. It uses a spec with several steps and constraints spanning all
// three destinations (step matcher, forbid, semantic) to exercise the ID
// bookkeeping across the whole compiler, not just one classification branch.
func TestContract_IDsArePreservedAndUnique(t *testing.T) {
	s := &spec.SkillSpec{
		Name:        "multi-step",
		Purpose:     "Do several things.",
		Description: "Does several things across steps.",
		Triggers:    []spec.TriggerPhrase{{Text: "do several things"}},
		Steps: []spec.Step{
			{ID: "step-a", Action: "Run scripts/one.py on the input.", Postcondition: "out/one.csv exists."},
			{ID: "step-b", Action: "Write out/report.json with the results.", Postcondition: "out/report.json exists."},
			{ID: "step-c", Action: "Read config/settings.yaml for options.", Postcondition: "settings are loaded."},
			{ID: "step-d", Action: "Run scripts/two.py to validate.", Postcondition: "exits 0.", Validation: true},
			{ID: "step-e", Action: "Politely thank the user for their patience.", Postcondition: "user feels thanked."},
		},
		Constraints: []spec.Rule{
			{ID: "rule-1", Text: "Never write outside out/.", Kind: spec.RuleMustNot, Severity: spec.SeverityCritical},
			{ID: "rule-2", Text: "Must not connect to the network.", Kind: spec.RuleMustNot, Severity: spec.SeverityMajor},
			{ID: "rule-3", Text: "Always be polite to the user.", Kind: spec.RuleMust, Severity: spec.SeverityMinor},
		},
		TargetTier: spec.TierMid,
	}

	c, err := contract.Compile(s)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	assertUnique := func(kind string, ids []string, wantIDs map[string]bool) {
		seen := make(map[string]bool)
		for _, id := range ids {
			if id == "" {
				t.Errorf("%s: empty ID emitted", kind)
				continue
			}
			if seen[id] {
				t.Errorf("%s: duplicate ID %q", kind, id)
			}
			seen[id] = true
			if !wantIDs[id] {
				t.Errorf("%s: unexpected ID %q not derived from any source step/constraint", kind, id)
			}
		}
	}

	stepIDs := make(map[string]bool)
	for _, st := range s.Steps {
		stepIDs[st.ID] = true
	}
	constraintIDs := make(map[string]bool)
	for _, cn := range s.Constraints {
		constraintIDs[cn.ID] = true
	}
	allIDs := make(map[string]bool)
	for id := range stepIDs {
		allIDs[id] = true
	}
	for id := range constraintIDs {
		allIDs[id] = true
	}

	var stepMatchIDs, forbidIDs, semanticIDs []string
	for _, sm := range c.Steps {
		stepMatchIDs = append(stepMatchIDs, sm.ID)
	}
	for _, f := range c.Forbid {
		forbidIDs = append(forbidIDs, f.ID)
	}
	for _, sem := range c.Semantic {
		semanticIDs = append(semanticIDs, sem.ID)
	}

	assertUnique("StepMatch", stepMatchIDs, allIDs)
	assertUnique("ForbidMatch", forbidIDs, allIDs)
	assertUnique("SemanticRule", semanticIDs, allIDs)

	// Every source step and constraint must be accounted for exactly once
	// across the three destinations (no item vanishes, none is duplicated
	// across destinations).
	occurrences := make(map[string]int)
	for _, id := range stepMatchIDs {
		occurrences[id]++
	}
	for _, id := range forbidIDs {
		occurrences[id]++
	}
	for _, id := range semanticIDs {
		occurrences[id]++
	}
	for id := range allIDs {
		if occurrences[id] != 1 {
			t.Errorf("source id %q appears %d times across Steps/Forbid/Semantic, want exactly 1", id, occurrences[id])
		}
	}
}
