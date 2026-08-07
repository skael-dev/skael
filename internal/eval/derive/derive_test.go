package derive_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/skael-dev/skael/internal/eval/derive"
	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/eval/runner"
	"github.com/skael-dev/skael/internal/eval/sandbox"
	"github.com/skael-dev/skael/internal/eval/spec"
	"github.com/skael-dev/skael/internal/eval/suite"
	"github.com/skael-dev/skael/internal/evalsuite"
	skillpkg "github.com/skael-dev/skael/internal/skill"
)

// derivedTaskCount mirrors derive's unexported taskCount: the fake gateway
// must draft exactly as many tasks as Derive asks GenerateN for, or
// runner.BuildPlan's dev/holdout arithmetic below is checked against the
// wrong denominator.
const derivedTaskCount = 18

// derivedSplitSeed mirrors derive's unexported splitSeed. The two paths (this
// test's fake driver and the real package) must agree on which tasks land in
// which split, or voidEveryHoldoutTask votes on the wrong set.
const derivedSplitSeed int64 = 1

// taskMarker is embedded as a comment line in every generated oracle and
// verifier script, so recordingDriver — which only ever sees a workspace path
// and an argv, never a task ID — can tell which task package it is running.
const taskMarker = "# skael-task-id: "

func taskID(n int) string { return fmt.Sprintf("t%02d", n) }

// holdoutTaskIDs runs the real split algorithm over the fake gateway's task
// IDs, rather than reimplementing Suite.Split's arithmetic — reusing suite.Split
// directly is what keeps this test from silently drifting out of sync with the
// production split.
var holdoutTaskIDs = func() map[string]bool {
	s := &suite.Suite{}
	for i := 1; i <= derivedTaskCount; i++ {
		s.Tasks = append(s.Tasks, suite.TaskPkg{ID: taskID(i)})
	}
	s.Split(derivedSplitSeed)
	out := map[string]bool{}
	for _, t := range s.Tasks {
		if t.Split == "holdout" {
			out[t.ID] = true
		}
	}
	return out
}()

// behaviourFunc decides one task's oracle gate outcome: the oracle's exit
// code, the post-oracle verifier's exit code, and the bare-workspace
// verifier's exit code. See suite.checkOne for what each of the three means.
type behaviourFunc func(id string) (oracleExit, verifierExit, bareVerifierExit int)

// allTasksPass is a suite whose every task clears the oracle gate cleanly.
func allTasksPass(string) (int, int, int) { return 0, 0, 1 }

// voidEveryHoldoutTask fails the oracle on every task Split assigns to
// holdout, leaving the dev set intact. Used to exercise BuildPlan's holdout
// floor.
func voidEveryHoldoutTask(id string) (int, int, int) {
	if holdoutTaskIDs[id] {
		return 1, 0, 1
	}
	return 0, 0, 1
}

// voidEveryThirdTask fails the oracle for every third task by sorted ID,
// independent of dev/holdout split — a spread that should still leave both
// splits with enough eligible tasks for tier "full".
func voidEveryThirdTask(id string) (int, int, int) {
	n, err := strconv.Atoi(strings.TrimPrefix(id, "t"))
	if err != nil {
		return 0, 0, 1
	}
	if n%3 == 0 {
		return 1, 0, 1
	}
	return 0, 0, 1
}

// fakeGatewayConfig is set up through fakeGatewayOption functions before the
// gateway is constructed.
type fakeGatewayConfig struct {
	envFragTask string
}

type fakeGatewayOption func(*fakeGatewayConfig)

// withGeneratedEnvFrag makes the fake gateway emit a Dockerfile fragment on
// the named task, so a test can exercise the oracle gate's voiding of tasks
// the single prepared image cannot serve.
func withGeneratedEnvFrag(id string) fakeGatewayOption {
	return func(c *fakeGatewayConfig) { c.envFragTask = id }
}

// fakeGateway is an llm.Gateway that answers derive's two calls: spec.recover
// (an already-valid spec, so no repair call is spent) and suite.draft (a
// fixed-size suite whose task IDs holdoutTaskIDs and voidEveryThirdTask both
// key off). Dispatch is on Req.Role rather than call order, so a change in
// how many calls a path costs does not silently reshuffle which response goes
// where.
type fakeGateway struct {
	mu    sync.Mutex
	calls []llm.Req
	cfg   fakeGatewayConfig
}

// newFakeGateway builds a fakeGateway. behaviour is accepted for symmetry
// with newTestDeriver (which threads the same behaviour into the driver) but
// does not affect what the gateway drafts — only the driver's oracle-gate
// verdicts vary per scenario.
func newFakeGateway(t *testing.T, _ behaviourFunc, opts ...fakeGatewayOption) *fakeGateway {
	t.Helper()
	var cfg fakeGatewayConfig
	for _, o := range opts {
		o(&cfg)
	}
	return &fakeGateway{cfg: cfg}
}

func (g *fakeGateway) Calls() []llm.Req {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]llm.Req(nil), g.calls...)
}

func (g *fakeGateway) Complete(_ context.Context, r llm.Req) (llm.Res, error) {
	g.mu.Lock()
	g.calls = append(g.calls, r)
	g.mu.Unlock()

	switch r.Role {
	case "spec.recover":
		return llm.Res{Text: recoveredSpecJSON(), Model: "fake"}, nil
	case "suite.draft":
		return llm.Res{Text: draftedSuiteJSON(g.cfg), Model: "fake"}, nil
	default:
		return llm.Res{}, fmt.Errorf("fakeGateway: unexpected role %q", r.Role)
	}
}

func (g *fakeGateway) ModelFor(llm.ModelClass) string { return "fake" }

// recoveredSpecJSON is a SkillSpec that already passes Validate, so
// spec.Recover spends exactly one call.
func recoveredSpecJSON() string {
	s := spec.SkillSpec{
		Name:        "demo",
		Purpose:     "Demonstrate the derive pipeline",
		Description: "A demo skill recovered from a bundle for tests.",
		Triggers: []spec.TriggerPhrase{
			{Text: "do the demo thing"},
			{Text: "do something unrelated", Negative: true},
		},
		Steps: []spec.Step{
			{ID: "s1", Action: "Do the thing", Postcondition: "The thing is done"},
		},
		TargetTier: spec.TierMid,
	}
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// draftScript is one oracle or verifier script body. It never really runs —
// recordingDriver decides exit codes from its behaviourFunc rather than by
// executing bash — but it carries taskMarker so the driver can identify which
// task package a workspace holds.
func draftScript(id string) string {
	return "#!/bin/bash\n" + taskMarker + id + "\nexit 0\n"
}

// draftedSuiteJSON drafts derivedTaskCount task packages with deterministic
// IDs, plus a trigger set large enough for tier "full"'s 16 probes (8
// positive, 8 negative). cfg.envFragTask, if set, gets a Dockerfile fragment.
func draftedSuiteJSON(cfg fakeGatewayConfig) string {
	type task struct {
		ID       string `json:"id"`
		Kind     string `json:"kind"`
		PromptMD string `json:"prompt_md"`
		EnvFrag  string `json:"env_frag,omitempty"`
		Oracle   string `json:"oracle"`
		Verifier string `json:"verifier"`
	}
	kinds := []string{"happy", "variant", "edge", "negative-trigger"}

	tasks := make([]task, 0, derivedTaskCount)
	for i := 1; i <= derivedTaskCount; i++ {
		id := taskID(i)
		var envFrag string
		if id == cfg.envFragTask {
			envFrag = "FROM ubuntu:22.04\nRUN apt-get update && apt-get install -y jq\n"
		}
		tasks = append(tasks, task{
			ID: id, Kind: kinds[i%len(kinds)], PromptMD: fmt.Sprintf("Do task %s.", id),
			EnvFrag: envFrag, Oracle: draftScript(id), Verifier: draftScript(id),
		})
	}

	positive := make([]string, 8)
	negative := make([]string, 8)
	for i := range positive {
		positive[i] = fmt.Sprintf("please help with the demo skill, case %d", i)
		negative[i] = fmt.Sprintf("please help with something adjacent but unrelated, case %d", i)
	}

	out := struct {
		Tasks    []task `json:"tasks"`
		Triggers struct {
			Positive []string `json:"positive"`
			Negative []string `json:"negative"`
		} `json:"triggers"`
	}{Tasks: tasks}
	out.Triggers.Positive = positive
	out.Triggers.Negative = negative

	b, err := json.Marshal(out)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// recordingDriver is a sandbox.Driver fake. It records every RunSpec.Workspace
// and answers Run according to behaviour, distinguishing the oracle run from
// the post-oracle verifier run (same workspace, second call) from the bare
// verifier run (a workspace that never saw an oracle call) — the same three
// phases suite.checkOne drives in production. It deliberately does not
// implement Sweep/EnsureBase, so it does not satisfy derive's imagePreparer
// interface: prepare() falls back to a zero ImageRef, which is all a fake
// needs.
type recordingDriver struct {
	behaviour behaviourFunc

	mu         sync.Mutex
	workspaces []string
	oracleRan  map[string]bool
}

func (d *recordingDriver) Name() string           { return "recording" }
func (d *recordingDriver) HardwareIsolated() bool { return false }

func (d *recordingDriver) Prepare(context.Context, sandbox.EnvSpec) (sandbox.ImageRef, error) {
	return sandbox.ImageRef{}, nil
}

func (d *recordingDriver) Snapshot(context.Context, sandbox.ImageRef) (sandbox.SnapshotRef, error) {
	return sandbox.SnapshotRef{}, nil
}

func (d *recordingDriver) Run(_ context.Context, rs sandbox.RunSpec) (sandbox.RunResult, error) {
	d.mu.Lock()
	d.workspaces = append(d.workspaces, rs.Workspace)
	if d.oracleRan == nil {
		d.oracleRan = map[string]bool{}
	}
	d.mu.Unlock()

	script := rs.Argv[len(rs.Argv)-1]
	id, err := readTaskMarker(rs.Workspace, script)
	if err != nil {
		return sandbox.RunResult{}, err
	}

	behaviour := d.behaviour
	if behaviour == nil {
		behaviour = allTasksPass
	}
	oracleExit, verifierExit, bareExit := behaviour(id)

	switch script {
	case "oracle/solve.sh":
		d.mu.Lock()
		d.oracleRan[rs.Workspace] = true
		d.mu.Unlock()
		return sandbox.RunResult{ExitCode: oracleExit}, nil
	case "verifier/test.sh":
		d.mu.Lock()
		ranOracle := d.oracleRan[rs.Workspace]
		d.mu.Unlock()
		if ranOracle {
			return sandbox.RunResult{ExitCode: verifierExit}, nil
		}
		return sandbox.RunResult{ExitCode: bareExit}, nil
	default:
		return sandbox.RunResult{}, fmt.Errorf("recordingDriver: unexpected script %q", script)
	}
}

// readTaskMarker recovers a task ID from the taskMarker comment line that
// draftScript wrote into the script at workspace/script.
func readTaskMarker(workspace, script string) (string, error) {
	b, err := os.ReadFile(filepath.Join(workspace, script))
	if err != nil {
		return "", fmt.Errorf("recordingDriver: reading %s: %w", script, err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, taskMarker) {
			return strings.TrimSpace(strings.TrimPrefix(line, taskMarker)), nil
		}
	}
	return "", fmt.Errorf("recordingDriver: no task marker in %s", script)
}

// newTestDeriver builds a Deriver over a fakeGateway and a recordingDriver,
// both driven by the same behaviour, staged under a fresh temp directory.
func newTestDeriver(t *testing.T, b behaviourFunc, opts ...fakeGatewayOption) *derive.Deriver {
	t.Helper()
	d, err := derive.New(derive.Options{
		Gateway:   newFakeGateway(t, b, opts...),
		Driver:    &recordingDriver{behaviour: b},
		StageRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("derive.New: %v", err)
	}
	return d
}

// writeFile creates dir/name with content, creating intermediate directories
// as needed.
func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// fixtureBundle packs a minimal published bundle: a SKILL.md and one
// scripts/ file, the same shape spec.Recover reads from.
func fixtureBundle(t *testing.T) []byte {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "SKILL.md", "---\nname: demo\ndescription: A demo skill for derive tests.\n---\n\n"+
		"Does demo things. Run scripts/run.sh.\n")
	writeFile(t, dir, "scripts/run.sh", "#!/bin/bash\necho demo\n")

	archive, _, _, err := skillpkg.Pack(dir)
	if err != nil {
		t.Fatalf("skill.Pack: %v", err)
	}
	return archive
}

func TestDerive_ProducesAnArchiveChecksAndSpec(t *testing.T) {
	d := newTestDeriver(t, allTasksPass)
	res, err := d.Derive(context.Background(), derive.Input{
		Skill: "demo", Bundle: fixtureBundle(t), Tier: "full", Panel: runner.DefaultPanel(),
	})
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if len(res.Archive) == 0 {
		t.Fatal("no archive returned")
	}
	if len(res.Checks) == 0 {
		// A suite with no recorded checks cannot tell a broken task from a
		// broken skill, and evalsuite.Put refuses it.
		t.Fatal("no checks returned")
	}
	if res.Spec == nil || res.Spec.Name != "demo" {
		t.Fatalf("spec = %+v, want one named demo", res.Spec)
	}
}

func TestDerive_VoidsTasksCarryingAnEnvFrag(t *testing.T) {
	// A per-task Dockerfile fragment cannot be applied by the single prepared
	// image. Dropping the fragment would run the task without its dependency
	// and blame the skill, so the task is voided instead.
	d := newTestDeriver(t, allTasksPass, withGeneratedEnvFrag("t03"))
	res, err := d.Derive(context.Background(), derive.Input{
		Skill: "demo", Bundle: fixtureBundle(t), Tier: "full", Panel: runner.DefaultPanel(),
	})
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	for _, c := range res.Checks {
		if c.TaskID == "t03" && !c.Void {
			t.Fatal("task with an env_frag was not voided")
		}
	}

	// Voiding the task in Checks is only half of it: the fragment must not be
	// in the archive either. cli/whetstone's RunEvalWith refuses an entire
	// suite that carries a single environment/Dockerfile.frag, so a packed
	// fragment makes the whole derived suite unevaluatable — the outcome
	// voiding was meant to avoid.
	dir := t.TempDir()
	if err := evalsuite.Unpack(res.Archive, dir); err != nil {
		t.Fatalf("unpack derived archive: %v", err)
	}
	loaded, err := suite.Load(dir)
	if err != nil {
		t.Fatalf("load derived suite: %v", err)
	}
	for _, task := range loaded.Tasks {
		if task.EnvFrag != "" {
			t.Fatalf("task %s still carries an environment fragment in the packed archive", task.ID)
		}
	}
	if err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Name() == "Dockerfile.frag" {
			t.Fatalf("the packed archive contains %s", path)
		}
		return nil
	}); err != nil {
		t.Fatalf("walk derived archive: %v", err)
	}
}

func TestDerive_RefusesASuiteBuildPlanWouldReject(t *testing.T) {
	// The gate is a dry run of the real planner, not a reimplementation of its
	// arithmetic — the two cannot drift apart, and the message is already the
	// right words.
	d := newTestDeriver(t, voidEveryHoldoutTask)
	_, err := d.Derive(context.Background(), derive.Input{
		Skill: "demo", Bundle: fixtureBundle(t), Tier: "full", Panel: runner.DefaultPanel(),
	})
	if err == nil {
		t.Fatal("Derive accepted a suite with no eligible holdout tasks")
	}
	if !strings.Contains(err.Error(), "holdout") {
		t.Fatalf("error %q does not name the failing split", err)
	}
}

func TestDerive_AcceptsASuiteWithVoidsSpreadEvenly(t *testing.T) {
	d := newTestDeriver(t, voidEveryThirdTask)
	if _, err := d.Derive(context.Background(), derive.Input{
		Skill: "demo", Bundle: fixtureBundle(t), Tier: "full", Panel: runner.DefaultPanel(),
	}); err != nil {
		t.Fatalf("Derive refused a suite BuildPlan would accept: %v", err)
	}
}

func TestDerive_StagesUnderTheConfiguredRunRoot(t *testing.T) {
	root := t.TempDir()
	drv := &recordingDriver{behaviour: allTasksPass}
	d, err := derive.New(derive.Options{
		Gateway: newFakeGateway(t, allTasksPass), Driver: drv, StageRoot: root,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := d.Derive(context.Background(), derive.Input{
		Skill: "demo", Bundle: fixtureBundle(t), Tier: "full", Panel: runner.DefaultPanel(),
	}); err != nil {
		// Must not be tolerated: a failed Derive creates no workspaces, and
		// the assertion below would then pass over an empty slice.
		t.Fatalf("Derive: %v", err)
	}
	if len(drv.workspaces) == 0 {
		t.Fatal("the oracle gate never ran")
	}
	for _, ws := range drv.workspaces {
		if !strings.HasPrefix(ws, root) {
			t.Fatalf("oracle workspace %q is outside the run root", ws)
		}
	}
}

func TestNew_RequiresAGatewayAndADriver(t *testing.T) {
	if _, err := derive.New(derive.Options{Driver: &recordingDriver{}}); err == nil {
		t.Fatal("New accepted a nil gateway")
	}
	if _, err := derive.New(derive.Options{Gateway: &fakeGateway{}}); err == nil {
		t.Fatal("New accepted a nil driver")
	}
}
