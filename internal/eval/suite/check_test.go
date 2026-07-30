package suite_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/skael-dev/skael/internal/eval/sandbox"
	"github.com/skael-dev/skael/internal/eval/suite"
)

// scriptDriver is a Driver that decides an exit code from the command it was
// asked to run, so a whole check can be exercised with no container.
type scriptDriver struct {
	exit func(argv []string) int
	runs [][]string
}

func (d *scriptDriver) Name() string           { return "script" }
func (d *scriptDriver) HardwareIsolated() bool { return false }
func (d *scriptDriver) Prepare(context.Context, sandbox.EnvSpec) (sandbox.ImageRef, error) {
	return sandbox.ImageRef{Tag: "t"}, nil
}
func (d *scriptDriver) Snapshot(context.Context, sandbox.ImageRef) (sandbox.SnapshotRef, error) {
	return sandbox.SnapshotRef{}, nil
}
func (d *scriptDriver) Run(_ context.Context, rs sandbox.RunSpec) (sandbox.RunResult, error) {
	d.runs = append(d.runs, rs.Argv)
	return sandbox.RunResult{ExitCode: d.exit(rs.Argv)}, nil
}

// recordingDriver is scriptDriver plus an onRun hook, so a test can inspect
// every RunSpec a check produced (workspace, network policy, ...). Exit codes
// mimic a well-formed task: the oracle solves it, the verifier accepts a
// solved workspace, and it rejects a bare one — checkOne runs a task's three
// phases in order (oracle, post-oracle verifier, bare verifier), so counting
// verifier calls modulo two distinguishes the two verifier runs.
type recordingDriver struct {
	onRun    func(rs sandbox.RunSpec)
	verifier int
}

func (d *recordingDriver) Name() string           { return "recording" }
func (d *recordingDriver) HardwareIsolated() bool { return false }
func (d *recordingDriver) Prepare(context.Context, sandbox.EnvSpec) (sandbox.ImageRef, error) {
	return sandbox.ImageRef{Tag: "t"}, nil
}
func (d *recordingDriver) Snapshot(context.Context, sandbox.ImageRef) (sandbox.SnapshotRef, error) {
	return sandbox.SnapshotRef{}, nil
}
func (d *recordingDriver) Run(_ context.Context, rs sandbox.RunSpec) (sandbox.RunResult, error) {
	if d.onRun != nil {
		d.onRun(rs)
	}
	if isOracle(rs.Argv) {
		return sandbox.RunResult{ExitCode: 0}, nil
	}
	d.verifier++
	if d.verifier%2 == 1 {
		return sandbox.RunResult{ExitCode: 0}, nil // post-oracle: accepts
	}
	return sandbox.RunResult{ExitCode: 1}, nil // bare: rejects
}

func twoTaskSuite() *suite.Suite {
	return &suite.Suite{Tasks: []suite.TaskPkg{
		{ID: "t1", Kind: "happy", PromptMD: "do it", Oracle: "#!/bin/sh\ntrue\n", Verifier: "#!/bin/sh\ntrue\n"},
		{ID: "t2", Kind: "edge", PromptMD: "do it oddly", Oracle: "#!/bin/sh\ntrue\n", Verifier: "#!/bin/sh\ntrue\n"},
	}}
}

func checkWith(t *testing.T, exit func(argv []string) int) ([]suite.CheckResult, *scriptDriver) {
	t.Helper()
	dir := t.TempDir()
	s := twoTaskSuite()
	if err := s.Write(dir); err != nil {
		t.Fatal(err)
	}
	d := &scriptDriver{exit: exit}
	rs, err := suite.Check(context.Background(), s, suite.CheckOptions{
		Driver: d, Image: sandbox.ImageRef{Tag: "t"}, SuiteDir: dir,
		Timeout: time.Minute, Concurrency: 1, Logger: func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	return rs, d
}

func isOracle(argv []string) bool {
	return strings.Contains(strings.Join(argv, " "), "oracle/solve.sh")
}
func isVerifier(argv []string) bool {
	return strings.Contains(strings.Join(argv, " "), "verifier/test.sh")
}

func TestCheck_PassesWhenTheOracleSolvesAndTheVerifierDiscriminates(t *testing.T) {
	// The bare verifier run is the second one for a task: it must fail, because
	// nothing has been done yet.
	seen := map[string]int{}
	rs, _ := checkWith(t, func(argv []string) int {
		if isVerifier(argv) {
			seen["v"]++
			if seen["v"]%2 == 0 {
				return 1 // the bare run
			}
		}
		return 0
	})
	for _, r := range rs {
		if r.Void {
			t.Errorf("task %s void: %s", r.TaskID, r.Reason)
		}
	}
}

func TestCheck_VoidsATaskWhoseOracleFails(t *testing.T) {
	rs, _ := checkWith(t, func(argv []string) int {
		if isOracle(argv) {
			return 1
		}
		return 1
	})
	for _, r := range rs {
		if !r.Void {
			t.Errorf("task %s not void despite a failing oracle", r.TaskID)
		}
		// A void task must say which side broke, or an author cannot tell a
		// broken oracle from a broken verifier.
		if !strings.Contains(r.Reason, "oracle") {
			t.Errorf("reason = %q, want it to name the oracle", r.Reason)
		}
	}
}

func TestCheck_VoidsATaskWhoseVerifierRejectsItsOwnOracle(t *testing.T) {
	rs, _ := checkWith(t, func(argv []string) int {
		if isVerifier(argv) {
			return 1
		}
		return 0
	})
	for _, r := range rs {
		if !r.Void || !strings.Contains(r.Reason, "verifier") {
			t.Errorf("task %s: void=%v reason=%q, want void naming the verifier", r.TaskID, r.Void, r.Reason)
		}
	}
}

func TestCheck_VoidsATaskWhoseVerifierPassesOnAnUntouchedWorkspace(t *testing.T) {
	// Every run succeeds, including the bare verifier — so the verifier is
	// asserting nothing about the work. A task like this hands a free pass to
	// every condition, inflating Reliability and flattening Uplift to a tie.
	rs, _ := checkWith(t, func([]string) int { return 0 })
	for _, r := range rs {
		if !r.Void {
			t.Errorf("task %s not void despite a verifier that passes on an empty workspace", r.TaskID)
		}
		if !strings.Contains(r.Reason, "without") {
			t.Errorf("reason = %q, want it to say the verifier passes without the oracle", r.Reason)
		}
	}
}

func TestCheck_RunsEachTaskInItsOwnWorkspaceWithNoNetwork(t *testing.T) {
	dir := t.TempDir()
	s := twoTaskSuite()
	if err := s.Write(dir); err != nil {
		t.Fatal(err)
	}
	var specs []sandbox.RunSpec
	d := &recordingDriver{onRun: func(rs sandbox.RunSpec) { specs = append(specs, rs) }}
	if _, err := suite.Check(context.Background(), s, suite.CheckOptions{
		Driver: d, Image: sandbox.ImageRef{Tag: "t"}, SuiteDir: dir, Timeout: time.Minute, Concurrency: 1,
	}); err != nil {
		t.Fatal(err)
	}

	seen := map[string]bool{}
	for _, rs := range specs {
		// An oracle with network access can fetch the answer, and then the task
		// measures the network rather than the skill.
		if rs.Network != sandbox.NetNone {
			t.Errorf("check run with network policy %q", rs.Network)
		}
		seen[rs.Workspace] = true
	}
	// One workspace per (task, phase). A shared workspace lets the oracle's
	// output satisfy the bare verifier run, which is the exact thing that run
	// exists to detect.
	if len(seen) != len(specs) {
		t.Errorf("%d runs shared %d workspaces", len(specs), len(seen))
	}
}
