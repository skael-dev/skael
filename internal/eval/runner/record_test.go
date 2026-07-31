package runner_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/skael-dev/skael/internal/eval/agent"
	"github.com/skael-dev/skael/internal/eval/runner"
	"github.com/skael-dev/skael/internal/eval/store"
	"github.com/skael-dev/skael/internal/eval/trajectory"
)

func TestWriteArtifacts_KeepsTheNativeStreamByteIdentical(t *testing.T) {
	dir := t.TempDir()
	raw := []byte("{\"type\":\"system\"}\n{\"type\":\"assistant\"}\n\x00binary\xff")
	a, err := runner.WriteArtifacts(dir, raw, nil, runner.Grading{Status: "ok"}, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(a.TranscriptPath)
	if err != nil {
		t.Fatal(err)
	}
	// The transcript is the record of what the CLI actually said. A parser bug
	// found six months later is diagnosable only if this was not normalized,
	// re-encoded, or truncated on the way to disk.
	if string(got) != string(raw) {
		t.Errorf("transcript was rewritten:\n%q\nwant\n%q", got, raw)
	}
}

func TestWriteArtifacts_EventsAreOnePerLineInOrder(t *testing.T) {
	dir := t.TempDir()
	events := []trajectory.Event{
		{Seq: 1, Type: trajectory.TypeShell, Name: "python3 scripts/parse.py"},
		{Seq: 2, Type: trajectory.TypeFileWrite, Paths: []string{"out/tables.md"}},
	}
	a, err := runner.WriteArtifacts(dir, nil, events, runner.Grading{Status: "ok"}, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(a.EventsPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("%d lines, want 2", len(lines))
	}
	for i, line := range lines {
		var e trajectory.Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("line %d is not one JSON event: %v", i, err)
		}
		if e.Seq != i+1 {
			t.Errorf("line %d has Seq %d; order is what the contract's ordering rules are scored against", i, e.Seq)
		}
	}

	round, err := runner.LoadEvents(a.EventsPath)
	if err != nil || len(round) != 2 {
		t.Fatalf("LoadEvents = %d events, %v", len(round), err)
	}
}

func TestLoadEvents_RoundTripsALineLargerThanTheDefaultScannerBuffer(t *testing.T) {
	dir := t.TempDir()
	// bufio.Scanner's default MaxScanTokenSize is 64KiB. A Paths entry from a
	// wide glob can exceed that on its own; a scanner that has not raised its
	// buffer stops silently at this line rather than erroring, dropping the
	// rest of the trajectory.
	bigPath := strings.Repeat("a", 100*1024)
	events := []trajectory.Event{
		{Seq: 1, Type: trajectory.TypeFileWrite, Paths: []string{bigPath}},
	}
	a, err := runner.WriteArtifacts(dir, nil, events, runner.Grading{Status: "ok"}, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}

	got, err := runner.LoadEvents(a.EventsPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("LoadEvents = %d events, want 1 (a line over the default 64KiB buffer was dropped)", len(got))
	}
	if len(got[0].Paths) != 1 || got[0].Paths[0] != bigPath {
		t.Error("LoadEvents did not round-trip the oversized Paths entry")
	}
}

func TestWriteArtifacts_ExcludesTheInstalledSkillFromOutputs(t *testing.T) {
	ws := t.TempDir()
	mustWrite(t, filepath.Join(ws, "out", "tables.md"), "| a |")
	mustWrite(t, filepath.Join(ws, ".claude", "skills", "demo", "SKILL.md"), "---\nname: demo\n---\n")

	a, err := runner.WriteArtifacts(t.TempDir(), nil, nil, runner.Grading{Status: "ok"}, ws, []string{".claude"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(a.OutputsDir, "out", "tables.md")); err != nil {
		t.Errorf("the verifier's inputs were not collected: %v", err)
	}
	// Sixty copies of the bundle under runs/ is most of the disk an eval uses,
	// and none of it is evidence: the bundle is already stored once.
	if _, err := os.Stat(filepath.Join(a.OutputsDir, ".claude")); err == nil {
		t.Error("the installed skill was copied into the run's outputs")
	}
}

func TestGrading_RoundTripsWithItsKeyAndMeta(t *testing.T) {
	dir := t.TempDir()
	verifierExit := 1
	g := runner.Grading{
		Key:          store.RunKey{TaskID: "t1", Agent: "claude-code", Model: "opus", Condition: "skill", Attempt: 2},
		VerifierExit: &verifierExit,
		Meta:         agent.Meta{AgentVersion: "2.1.220", InputTokens: 1200, OutputTokens: 800, NumTurns: 7},
		Status:       "ok",
		StartedAt:    time.Unix(1700000000, 0).UTC(),
		FinishedAt:   time.Unix(1700000100, 0).UTC(),
	}
	a, err := runner.WriteArtifacts(dir, nil, nil, g, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := runner.LoadGrading(a.GradingPath)
	if err != nil {
		t.Fatal(err)
	}
	// Token counts feed Efficiency and the agent version is what attributes a
	// score to a CLI build. Losing either makes a score unexplainable later.
	if got.Meta.InputTokens != 1200 || got.Meta.AgentVersion != "2.1.220" || got.VerifierExit == nil || *got.VerifierExit != 1 {
		t.Errorf("grading round-trip lost data: %+v", got)
	}
	if got.Key != g.Key {
		t.Errorf("key = %+v, want %+v", got.Key, g.Key)
	}
}

// TestGrading_NilVerifierExitRoundTripsAsNil pins the distinction a nullable
// VerifierExit exists for: "the verifier never ran" (nil) must not collapse
// into "the verifier ran and exited 0" (a pointer to zero) anywhere along the
// path grading.json takes to LoadGrading.
func TestGrading_NilVerifierExitRoundTripsAsNil(t *testing.T) {
	dir := t.TempDir()
	g := runner.Grading{
		Key:    store.RunKey{TaskID: "t1", Agent: "claude-code", Model: "opus", Condition: "trigger", Attempt: 1},
		Status: "error",
	}
	a, err := runner.WriteArtifacts(dir, nil, nil, g, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := runner.LoadGrading(a.GradingPath)
	if err != nil {
		t.Fatal(err)
	}
	if got.VerifierExit != nil {
		t.Errorf("VerifierExit = %v, want nil (the verifier never ran)", *got.VerifierExit)
	}
}

// mustWrite creates path's parent directories and writes content, failing the
// test on any error.
func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
