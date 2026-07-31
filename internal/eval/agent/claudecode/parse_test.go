package claudecode_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/agent"
	"github.com/skael-dev/skael/internal/eval/agent/claudecode"
	"github.com/skael-dev/skael/internal/eval/trajectory"
)

func parseFixture(t *testing.T, name string) *agent.Result {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()

	res, err := claudecode.New().Parse(f)
	if err != nil {
		t.Fatalf("Parse(%s): %v", name, err)
	}
	return res
}

func typesOf(events []trajectory.Event) []trajectory.EventType {
	out := make([]trajectory.EventType, len(events))
	for i, e := range events {
		out[i] = e.Type
	}
	return out
}

func countType(events []trajectory.Event, want trajectory.EventType) int {
	var n int
	for _, e := range events {
		if e.Type == want {
			n++
		}
	}
	return n
}

func TestParse_MapsToolsToNormalizedTypes(t *testing.T) {
	res := parseFixture(t, "basic-tools.jsonl")

	if countType(res.Events, trajectory.TypeFileWrite) < 1 {
		t.Errorf("no file_write event parsed from a fixture that writes a file: %v", typesOf(res.Events))
	}
	if countType(res.Events, trajectory.TypeFileRead) < 1 {
		t.Errorf("no file_read event parsed from a fixture that reads a file: %v", typesOf(res.Events))
	}
	if countType(res.Events, trajectory.TypeMessage) < 1 {
		t.Errorf("no message event parsed: %v", typesOf(res.Events))
	}

	// The written path must be captured — contract forbid rules match on paths,
	// so a file_write with no path is unscoreable.
	var found bool
	for _, e := range res.Events {
		if e.Type == trajectory.TypeFileWrite && len(e.Paths) > 0 {
			found = true
		}
	}
	if !found {
		t.Error("file_write event carried no paths; forbid rules match on paths")
	}
}

func TestParse_SeqIsDenseAndMonotonic(t *testing.T) {
	res := parseFixture(t, "basic-tools.jsonl")
	for i, e := range res.Events {
		if e.Seq != i+1 {
			t.Fatalf("event %d has Seq %d; Seq must be dense and 1-based for Kendall-tau ordering", i, e.Seq)
		}
	}
}

func TestParse_TimestampsCarryForward(t *testing.T) {
	// Only assistant and user events carry a timestamp. Events that do not must
	// inherit the last seen one rather than defaulting to the zero time, which
	// would make duration arithmetic produce values from year 1.
	res := parseFixture(t, "basic-tools.jsonl")
	for _, e := range res.Events {
		if e.TS.IsZero() {
			t.Errorf("event %d (%s) has a zero timestamp", e.Seq, e.Type)
		}
	}
}

func TestParse_NoTimestampAnywhereStaysZeroNotFabricated(t *testing.T) {
	// A session that dies before its first assistant/user turn — an auth
	// failure inside the sandbox, an early crash, a stream truncated at the
	// first line — carries no timestamp anywhere. The parser must not invent
	// a wall-clock value to paper over that: a fabricated timestamp (e.g.
	// time.Now()) would make Parse non-deterministic across runs on the same
	// recorded stream, which breaks the reproducibility scores depend on. The
	// zero time.Time is the honest, detectable signal that no time
	// information exists — assert that directly, not merely that Parse
	// doesn't panic.
	r := stringReader(
		`{"type":"system","subtype":"init","session_id":"x"}` + "\n" +
			`{"type":"rate_limit_event"}` + "\n" +
			`{"type":"some_future_event","session_id":"x"}` + "\n")

	res, err := claudecode.New().Parse(r)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(res.Events) != 3 {
		t.Fatalf("want 3 events, got %d: %v", len(res.Events), typesOf(res.Events))
	}
	for i, e := range res.Events {
		if !e.TS.IsZero() {
			t.Errorf("event %d TS = %v, want the zero time — no timestamp exists anywhere in this stream, so any non-zero value would be fabricated", i, e.TS)
		}
	}
}

func TestParse_SkillInvocationBecomesSkillRead(t *testing.T) {
	res := parseFixture(t, "skill-invocation.jsonl")

	var got []string
	for _, e := range res.Events {
		if e.Type == trajectory.TypeSkillRead {
			got = append(got, e.Name)
		}
	}
	if len(got) == 0 {
		t.Fatalf("no skill_read event parsed; TriggerF1 is the highest-weighted pillar and depends on this: %v", typesOf(res.Events))
	}
	if got[0] != "find-skills" {
		t.Errorf("skill_read Name = %q, want the invoked skill name %q", got[0], "find-skills")
	}
}

func TestParse_UnknownEventsBecomeOpaque(t *testing.T) {
	// hook_started / hook_response / rate_limit_event / init / result are all
	// stream bookkeeping, not agent behaviour. They must be recorded as opaque
	// so they are excluded from contract denominators rather than scored.
	res := parseFixture(t, "basic-tools.jsonl")
	if countType(res.Events, trajectory.TypeOpaque) == 0 {
		t.Error("expected the hook/init/result events to be recorded as opaque")
	}
	for _, e := range res.Events {
		if e.Type == trajectory.TypeOpaque && e.Name == "" {
			t.Errorf("opaque event %d has no Name; an unmapped event must record what it was", e.Seq)
		}
	}
}

func TestParse_ExtractsMeta(t *testing.T) {
	res := parseFixture(t, "basic-tools.jsonl")

	if res.Meta.AgentVersion == "" {
		t.Error("Meta.AgentVersion empty; the version stamp is what makes CLI churn diagnosable")
	}
	if res.Meta.Model == "" {
		t.Error("Meta.Model empty; the panel matrix records the resolved model per run")
	}
	if res.Meta.OutputTokens <= 0 {
		t.Errorf("Meta.OutputTokens = %d; Efficiency is a token ratio and needs real counts", res.Meta.OutputTokens)
	}
	if res.Meta.DurationMS <= 0 {
		t.Errorf("Meta.DurationMS = %d", res.Meta.DurationMS)
	}
	if len(res.Meta.VisibleSkills) == 0 {
		t.Error("Meta.VisibleSkills empty; trigger precision needs to know which skills were offered")
	}
	if res.Meta.IsError {
		t.Error("Meta.IsError true on a successful fixture")
	}
}

func TestParse_PolymorphicToolResultDoesNotFail(t *testing.T) {
	// tool_use_result is an object on success, a bare string on tool error, and
	// null sometimes. The skill-invocation fixture contains string-valued
	// results from denied Bash commands; parsing must survive all three.
	res := parseFixture(t, "skill-invocation.jsonl")
	if len(res.Events) == 0 {
		t.Fatal("no events parsed")
	}
	if countType(res.Events, trajectory.TypeToolResult) == 0 {
		t.Errorf("no tool_result events: %v", typesOf(res.Events))
	}
}

func TestParse_MalformedLinesAreSkippedNotFatal(t *testing.T) {
	r := stringReader("{not json\n" +
		`{"type":"assistant","timestamp":"2026-07-29T19:55:21.430Z","message":{"role":"assistant","content":[{"type":"text","text":"hi"}]}}` + "\n")

	res, err := claudecode.New().Parse(r)
	if err != nil {
		t.Fatalf("Parse should tolerate a malformed line, got %v", err)
	}
	if countType(res.Events, trajectory.TypeMessage) != 1 {
		t.Errorf("valid line after a malformed one was not parsed: %v", typesOf(res.Events))
	}
}

func TestParse_UnrecognisedTopLevelTypeBecomesOpaque(t *testing.T) {
	// A stream-json type this parser has never seen (a future CLI addition)
	// must still surface as a recorded opaque event, not be silently dropped —
	// dropping it would shrink Meta.NumTurns-adjacent bookkeeping and the event
	// count in ways nothing downstream could detect.
	r := stringReader(`{"type":"some_future_event","session_id":"x","timestamp":"2026-07-29T19:55:21.430Z"}` + "\n")

	res, err := claudecode.New().Parse(r)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(res.Events) != 1 {
		t.Fatalf("want 1 event, got %d: %v", len(res.Events), typesOf(res.Events))
	}
	e := res.Events[0]
	if e.Type != trajectory.TypeOpaque {
		t.Errorf("Type = %q, want %q", e.Type, trajectory.TypeOpaque)
	}
	if e.Name != "some_future_event" {
		t.Errorf("Name = %q, want the unrecognised type recorded", e.Name)
	}
}

func TestCaps(t *testing.T) {
	c := claudecode.New().Caps()
	if c.SkillDir != ".claude/skills" {
		t.Errorf("SkillDir = %q, want .claude/skills", c.SkillDir)
	}
	if !c.SupportsSkillInvocation {
		t.Error("Claude Code exposes a Skill tool; SupportsSkillInvocation must be true")
	}
	if c.ModelFlag == "" {
		t.Error("ModelFlag empty; the panel varies models via this flag")
	}
}

func TestInvokeIsNotImplementedYet(t *testing.T) {
	_, err := claudecode.New().Invoke(context.TODO(), agent.InvokeSpec{})
	if err != agent.ErrInvokeNotImplemented {
		t.Errorf("Invoke err = %v, want ErrInvokeNotImplemented", err)
	}
}

func stringReader(s string) *strings.Reader { return strings.NewReader(s) }
