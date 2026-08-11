package agent_test

import (
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/agent"
	"github.com/skael-dev/skael/internal/eval/score"
	"github.com/skael-dev/skael/internal/eval/trajectory"
)

func parseFixture(t *testing.T, name string) *agent.Result {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()

	res, err := agent.New().Parse(f)
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

	res, err := agent.New().Parse(r)
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

// TestParse_RateLimitEventOnlyFlagsAnActualLimit pins the fix for a real
// defect: a Claude Code session emits rate_limit_event lines routinely as
// telemetry, most carrying status "allowed", and an earlier version of the
// parser set Meta.RateLimited on the event's mere presence. That made an
// ordinary session indistinguishable from one actually being throttled, so
// the runner burned its retries and failed sessions that were never rate
// limited — see internal/eval/agent/parse.go's rateLimitInfo
// comment, and tests/whetstone/e2e_docker_test.go's stubClaudeBaseTag, which
// hit this same defect against a real recorded transcript and had to strip
// the line to work around it before this fix landed.
func TestParse_RateLimitEventOnlyFlagsAnActualLimit(t *testing.T) {
	cases := []struct {
		name string
		line string
		want bool
	}{
		{
			name: "status allowed is not a hit",
			line: `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed"}}`,
			want: false,
		},
		{
			name: "a non-allowed status is a hit",
			line: `{"type":"rate_limit_event","rate_limit_info":{"status":"rejected"}}`,
			want: true,
		},
		{
			name: "no payload at all stays the conservative default",
			line: `{"type":"rate_limit_event"}`,
			want: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := agent.New().Parse(stringReader(tc.line + "\n"))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if res.Meta.RateLimited != tc.want {
				t.Errorf("RateLimited = %v, want %v", res.Meta.RateLimited, tc.want)
			}
		})
	}
}

func TestParse_SkillInvocationBecomesAnExplicitToolCall(t *testing.T) {
	res := parseFixture(t, "skill-invocation.jsonl")

	var got []trajectory.Event
	for _, e := range res.Events {
		if e.Type == trajectory.TypeToolCall && e.Name == "Skill" {
			got = append(got, e)
		}
	}
	if len(got) == 0 {
		t.Fatalf("no explicit Skill tool_call event parsed; TriggerF1 is the highest-weighted pillar and depends on this: %v", typesOf(res.Events))
	}
	found := false
	for _, p := range got[0].Paths {
		if path.Base(path.Dir(p)) == "find-skills" {
			found = true
		}
	}
	if !found {
		t.Errorf("Skill tool_call Paths = %v, want a path whose parent directory is the invoked skill %q", got[0].Paths, "find-skills")
	}
	// The native Skill invocation is an explicit "used this skill" signal, not a
	// mere read of its SKILL.md — it must never be reported as skill_read, or
	// score.DetectFiring's inferred branch becomes reachable from a real
	// invocation and Probe.Inferred stops meaning what it claims.
	for _, e := range res.Events {
		if e.Type == trajectory.TypeSkillRead {
			t.Errorf("skill invocation reported as skill_read: %+v", e)
		}
	}
}

func TestParse_SkillInvocationDetectedAsNonInferred(t *testing.T) {
	// Pins the fix for the previously-unreachable explicit branch of
	// score.DetectFiring: a real Claude Code Skill invocation, run through the
	// full parse, must be detected as fired and NOT inferred on an adapter that
	// supports native skill invocation. Collapsing the tool_call/skill_read
	// distinction back together (e.g. reverting the "Skill" case in mapToolUse
	// to emit TypeSkillRead) makes this fail.
	res := parseFixture(t, "skill-invocation.jsonl")
	caps := agent.Caps{SupportsSkillInvocation: true}

	fired, inferred := score.DetectFiring("find-skills", caps, res.Events)
	if !fired {
		t.Fatal("DetectFiring did not see the skill fire")
	}
	if inferred {
		t.Error("DetectFiring reported an explicit invocation as inferred")
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

	res, err := agent.New().Parse(r)
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

	res, err := agent.New().Parse(r)
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
	c := agent.New().Caps()
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

func stringReader(s string) *strings.Reader { return strings.NewReader(s) }
