package score_test

import (
	"math"
	"testing"

	"github.com/skael-dev/skael/internal/eval/agent"
	"github.com/skael-dev/skael/internal/eval/score"
	"github.com/skael-dev/skael/internal/eval/trajectory"
)

func TestTriggerF1_PerfectDiscrimination(t *testing.T) {
	ps := []score.Probe{
		{Positive: true, Fired: true}, {Positive: true, Fired: true},
		{Positive: false, Fired: false}, {Positive: false, Fired: false},
	}
	r, err := score.TriggerF1(ps)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(r.F1-1) > 1e-9 {
		t.Errorf("F1 = %v, want 1", r.F1)
	}
}

func TestTriggerF1_CountsEachQuadrant(t *testing.T) {
	ps := []score.Probe{
		{Positive: true, Fired: true},   // TP
		{Positive: true, Fired: false},  // FN — the under-triggering failure
		{Positive: false, Fired: true},  // FP — fired on a hard negative
		{Positive: false, Fired: false}, // TN
	}
	r, err := score.TriggerF1(ps)
	if err != nil {
		t.Fatal(err)
	}
	if r.TP != 1 || r.FN != 1 || r.FP != 1 || r.TN != 1 {
		t.Errorf("quadrants = %+v", r)
	}
	// P = R = 0.5, so F1 = 0.5.
	if math.Abs(r.F1-0.5) > 1e-9 {
		t.Errorf("F1 = %v, want 0.5", r.F1)
	}
}

func TestTriggerF1_ASkillThatNeverFiresScoresZero(t *testing.T) {
	ps := []score.Probe{
		{Positive: true, Fired: false}, {Positive: true, Fired: false},
		{Positive: false, Fired: false},
	}
	r, err := score.TriggerF1(ps)
	if err != nil {
		t.Fatal(err)
	}
	// Precision is undefined with no predictions. Defining it as 0 rather than 1
	// is the deliberate choice: a skill that never fires is worth nothing, and
	// the vacuously-perfect reading would hand it the highest precision in the
	// repository.
	if r.Precision != 0 || r.F1 != 0 {
		t.Errorf("precision = %v, F1 = %v, want both 0", r.Precision, r.F1)
	}
}

func TestTriggerF1_UnknownProbesLeaveTheDenominatorsAndAreReported(t *testing.T) {
	ps := []score.Probe{
		{Positive: true, Fired: true},
		{Positive: true, Unknown: true, Reason: "session errored"},
		{Positive: false, Fired: false},
	}
	r, err := score.TriggerF1(ps)
	if err != nil {
		t.Fatal(err)
	}
	// A probe whose session failed is not a miss. Counting it as one penalises
	// the skill for infrastructure, and the recall it produces is wrong in the
	// direction nobody checks.
	if r.Unknown != 1 {
		t.Errorf("Unknown = %d, want 1", r.Unknown)
	}
	if r.FN != 0 {
		t.Errorf("FN = %d, want 0: an unknown probe is not a false negative", r.FN)
	}
	if math.Abs(r.F1-1) > 1e-9 {
		t.Errorf("F1 = %v, want 1 over the two measurable probes", r.F1)
	}
}

func TestTriggerF1_RefusesASetWithNoPositives(t *testing.T) {
	// Recall over zero positives is undefined, and a trigger measurement with
	// nothing to fire on measures nothing.
	if _, err := score.TriggerF1([]score.Probe{{Positive: false, Fired: false}}); err == nil {
		t.Error("TriggerF1 returned a value with no positive probes")
	}
}

func TestTriggerF1_FlagsAnInferredMeasurement(t *testing.T) {
	r, err := score.TriggerF1([]score.Probe{
		{Positive: true, Fired: true, Inferred: true},
		{Positive: false, Fired: false},
	})
	if err != nil {
		t.Fatal(err)
	}
	// An explicit invocation event and a read of SKILL.md are different
	// measurements: a read cannot distinguish "consulted" from "used". Mixing
	// them without a label is the same mistake event_source exists to prevent
	// on the analytics side, so the result carries the flag to the report.
	if !r.AnyInferred {
		t.Error("AnyInferred = false despite an inferred probe")
	}
}

func TestDetectFiring_PrefersAnExplicitInvocation(t *testing.T) {
	caps := agent.Caps{SupportsSkillInvocation: true}
	events := []trajectory.Event{
		{Seq: 1, Type: trajectory.TypeToolCall, Name: "Skill", ArgsDigest: "x", Paths: []string{".claude/skills/demo/SKILL.md"}},
	}
	fired, inferred := score.DetectFiring("demo", caps, events)
	if !fired || inferred {
		t.Errorf("fired=%v inferred=%v, want an explicit detection", fired, inferred)
	}
}

func TestDetectFiring_FallsBackToASkillRead(t *testing.T) {
	caps := agent.Caps{SupportsSkillInvocation: false}
	events := []trajectory.Event{
		{Seq: 1, Type: trajectory.TypeSkillRead, Name: "demo", Paths: []string{".claude/skills/demo/SKILL.md"}},
	}
	fired, inferred := score.DetectFiring("demo", caps, events)
	if !fired || !inferred {
		t.Errorf("fired=%v inferred=%v, want an inferred detection", fired, inferred)
	}
}

func TestDetectFiring_IgnoresADistractorsSkillRead(t *testing.T) {
	caps := agent.Caps{SupportsSkillInvocation: false}
	events := []trajectory.Event{
		{Seq: 1, Type: trajectory.TypeSkillRead, Name: "other", Paths: []string{".claude/skills/other/SKILL.md"}},
	}
	// Reading a distractor is the correct behaviour on a hard negative. Counting
	// it as the skill under test firing would make precision measure nothing.
	if fired, _ := score.DetectFiring("demo", caps, events); fired {
		t.Error("a distractor's skill read counted as the skill firing")
	}
}

func TestDetectFiring_IgnoresTheSkillNameAsANonTerminalPathSegment(t *testing.T) {
	caps := agent.Caps{SupportsSkillInvocation: false}
	events := []trajectory.Event{
		{Seq: 1, Type: trajectory.TypeSkillRead, Name: "other-skill", Paths: []string{".claude/skills/other-skill/demo/subdir/notes.md"}},
	}
	// A distractor whose own directory happens to contain a subfolder named
	// after the skill under test is a plausible fixture-naming collision, not
	// a firing of that skill. Only the immediate parent directory of the path
	// counts, so "demo" appearing as a non-terminal segment here must not
	// match.
	if fired, _ := score.DetectFiring("demo", caps, events); fired {
		t.Error("the skill name as a non-terminal path segment counted as the skill firing")
	}
}
