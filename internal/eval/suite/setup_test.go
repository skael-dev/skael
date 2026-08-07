package suite_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/skael-dev/skael/internal/eval/sandbox"
	"github.com/skael-dev/skael/internal/eval/suite"
)

const fixtureSetup = "mkdir -p data\nprintf 'id\\n1\\n' > data/users.csv\n"

func setupTaskSuite() *suite.Suite {
	return &suite.Suite{Tasks: []suite.TaskPkg{{
		ID: "t1", Kind: "happy", PromptMD: "convert data/users.csv",
		Setup: fixtureSetup, Oracle: "#!/bin/sh\ntrue\n", Verifier: "#!/bin/sh\ntrue\n",
	}}}
}

// The script is executed directly by the gate and the runner, so its
// executable bit has to survive Write exactly as the oracle's does.
// TestWriteAndLoad_RoundTripsSetup covers the content round trip.
func TestWrite_SetupScriptIsExecutable(t *testing.T) {
	dir := t.TempDir()
	if err := setupTaskSuite().Write(dir); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(filepath.Join(dir, "tasks", "t1", "environment", "setup.sh"))
	if err != nil {
		t.Fatalf("setup script not written: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("setup.sh is not executable: %v", info.Mode().Perm())
	}
}

// A suite written before setup.sh existed carries the same shell under the
// old name. Those files are what a real generated suite already holds, so
// loading them as the setup script is what keeps an existing workspace
// runnable rather than requiring a regeneration.
func TestLoad_ReadsALegacyDockerfileFrag(t *testing.T) {
	dir := t.TempDir()
	if err := setupTaskSuite().Write(dir); err != nil {
		t.Fatal(err)
	}
	envDir := filepath.Join(dir, "tasks", "t1", "environment")
	if err := os.Remove(filepath.Join(envDir, "setup.sh")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(envDir, "Dockerfile.frag"), []byte(fixtureSetup), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := suite.Load(dir)
	if err != nil {
		t.Fatalf("a suite carrying a legacy fragment failed to load: %v", err)
	}
	if got.Tasks[0].Setup != fixtureSetup {
		t.Fatalf("legacy fragment was not loaded as the setup script: %q", got.Tasks[0].Setup)
	}
}

// A legacy suite's script is on disk under the old name, so staging the task
// tree verbatim gives the workspace no setup.sh. The gate must still run the
// script — which it can, because Load recovered it — or every already-
// generated suite fails on a missing file instead of on its own merits.
func TestCheck_RunsALegacySuitesSetupScript(t *testing.T) {
	dir := t.TempDir()
	s := setupTaskSuite()
	if err := s.Write(dir); err != nil {
		t.Fatal(err)
	}
	envDir := filepath.Join(dir, "tasks", "t1", "environment")
	if err := os.Rename(filepath.Join(envDir, "setup.sh"), filepath.Join(envDir, "Dockerfile.frag")); err != nil {
		t.Fatal(err)
	}

	loaded, err := suite.Load(dir)
	if err != nil {
		t.Fatal(err)
	}

	// present counts the setup runs that found the script actually staged in
	// the workspace — a run against a missing file would be no fix at all.
	var ran, present int
	d := &statDriver{onRun: func(rs sandbox.RunSpec) {
		if !isSetup(rs.Argv) {
			return
		}
		ran++
		if _, err := os.Stat(filepath.Join(rs.Workspace, suite.SetupScript)); err == nil {
			present++
		}
	}}
	if _, err := suite.Check(context.Background(), loaded, suite.CheckOptions{
		Driver: d, Image: sandbox.ImageRef{Tag: "t"}, SuiteDir: dir,
		Timeout: time.Minute, Concurrency: 1, Logger: func(string, ...any) {},
	}); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if ran == 0 {
		t.Fatal("a legacy suite's setup script never ran")
	}
	if present != ran {
		t.Fatalf("%d of %d setup runs had no %s staged in the workspace", ran-present, ran, suite.SetupScript)
	}
}

// statDriver exposes the whole RunSpec, so a test can inspect the workspace a
// run was handed rather than only the command.
type statDriver struct {
	onRun func(sandbox.RunSpec)
}

func (d *statDriver) Name() string           { return "stat" }
func (d *statDriver) HardwareIsolated() bool { return false }
func (d *statDriver) Prepare(context.Context, sandbox.EnvSpec) (sandbox.ImageRef, error) {
	return sandbox.ImageRef{Tag: "t"}, nil
}
func (d *statDriver) Snapshot(context.Context, sandbox.ImageRef) (sandbox.SnapshotRef, error) {
	return sandbox.SnapshotRef{}, nil
}
func (d *statDriver) Run(_ context.Context, rs sandbox.RunSpec) (sandbox.RunResult, error) {
	d.onRun(rs)
	return sandbox.RunResult{ExitCode: 0}, nil
}

func isSetup(argv []string) bool {
	return strings.Contains(strings.Join(argv, " "), "environment/setup.sh")
}

// Both workspaces need the fixtures: the bare run exists to prove the
// verifier rejects an unsolved workspace, and a bare workspace missing the
// task's own input files would fail for a different reason entirely.
func TestCheck_RunsSetupInBothWorkspaces(t *testing.T) {
	dir := t.TempDir()
	s := setupTaskSuite()
	if err := s.Write(dir); err != nil {
		t.Fatal(err)
	}

	var order []string
	d := &scriptDriver{exit: func(argv []string) int {
		switch {
		case isSetup(argv):
			order = append(order, "setup")
			return 0
		case isOracle(argv):
			order = append(order, "oracle")
			return 0
		default:
			order = append(order, "verifier")
			if len(order) > 0 && order[len(order)-2] == "oracle" {
				return 0
			}
			return 1
		}
	}}
	rs, err := suite.Check(context.Background(), s, suite.CheckOptions{
		Driver: d, Image: sandbox.ImageRef{Tag: "t"}, SuiteDir: dir,
		Timeout: time.Minute, Concurrency: 1, Logger: func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if rs[0].Void {
		t.Fatalf("a task with a setup script was voided: %s", rs[0].Reason)
	}

	want := []string{"setup", "oracle", "verifier", "setup", "verifier"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("run order was %v, want %v", order, want)
	}
}

// A setup script that cannot produce the task's inputs makes the task
// unrunnable, which is the gate's business to catch — the same class of
// answer as an oracle that does not solve it.
func TestCheck_VoidsATaskWhoseSetupFails(t *testing.T) {
	dir := t.TempDir()
	s := setupTaskSuite()
	if err := s.Write(dir); err != nil {
		t.Fatal(err)
	}

	d := &scriptDriver{exit: func(argv []string) int {
		if isSetup(argv) {
			return 1
		}
		return 0
	}}
	rs, err := suite.Check(context.Background(), s, suite.CheckOptions{
		Driver: d, Image: sandbox.ImageRef{Tag: "t"}, SuiteDir: dir,
		Timeout: time.Minute, Concurrency: 1, Logger: func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !rs[0].Void {
		t.Fatal("a task whose setup script failed was not voided")
	}
	if !strings.Contains(rs[0].Reason, "setup") {
		t.Fatalf("the void reason does not name the setup script: %q", rs[0].Reason)
	}
}

// A task with no setup script is the common case and must not cost a run.
func TestCheck_SkipsSetupWhenATaskHasNone(t *testing.T) {
	rs, d := checkWith(t, func([]string) int {
		return 0
	})
	for _, argv := range d.runs {
		if isSetup(argv) {
			t.Fatalf("a task with no setup script still ran one: %v", argv)
		}
	}
	if len(rs) == 0 {
		t.Fatal("no results")
	}
}
