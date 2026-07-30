package runner_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/skael-dev/skael/internal/eval/agent"
	"github.com/skael-dev/skael/internal/eval/runner"
	"github.com/skael-dev/skael/internal/eval/sandbox"
	"github.com/skael-dev/skael/internal/eval/store"
	"github.com/skael-dev/skael/internal/eval/suite"
	"github.com/skael-dev/skael/internal/eval/trajectory"
)

// fakeAdapter records what it was asked to do and replays a canned stream.
type fakeAdapter struct {
	mu        sync.Mutex
	installs  []string
	invokes   []agent.InvokeSpec
	stream    string
	meta      agent.Meta
	invokeErr error
	// rateLimitFirst makes the first invocation report a rate limit, so the
	// backoff path is exercised without a real 429.
	rateLimitFirst atomic.Bool
}

func (f *fakeAdapter) Name() string { return "claude-code" }
func (f *fakeAdapter) Caps() agent.Caps {
	return agent.Caps{EventTier: "A", ModelFlag: "--model", SkillDir: ".claude/skills", SupportsSkillInvocation: true}
}
func (f *fakeAdapter) InstallSkill(ws, bundle string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.installs = append(f.installs, ws)
	return nil
}
func (f *fakeAdapter) Invoke(_ context.Context, s agent.InvokeSpec) (agent.RawStream, error) {
	f.mu.Lock()
	f.invokes = append(f.invokes, s)
	f.mu.Unlock()
	if f.invokeErr != nil {
		return nil, f.invokeErr
	}
	return strings.NewReader(f.stream), nil
}
func (f *fakeAdapter) Parse(agent.RawStream) (*agent.Result, error) {
	m := f.meta
	if f.rateLimitFirst.CompareAndSwap(true, false) {
		m.RateLimited = true
	}
	return &agent.Result{
		Events: []trajectory.Event{{Seq: 1, Type: trajectory.TypeSkillRead, Name: "demo", Paths: []string{"SKILL.md"}}},
		Meta:   m,
	}, nil
}

func (f *fakeAdapter) invokeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.invokes)
}

func (f *fakeAdapter) installCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.installs)
}

// recordingDriver is a Driver that records every RunSpec it was asked to run
// and calls onRun, if set, synchronously from within Run — tests use it to
// observe or perturb concurrency and per-session failures.
type recordingDriver struct {
	mu       sync.Mutex
	isolated bool
	runs     []sandbox.RunSpec
	onRun    func(sandbox.RunSpec)
}

func (d *recordingDriver) Name() string           { return "recording" }
func (d *recordingDriver) HardwareIsolated() bool { return d.isolated }
func (d *recordingDriver) Prepare(context.Context, sandbox.EnvSpec) (sandbox.ImageRef, error) {
	return sandbox.ImageRef{Tag: "recording:latest"}, nil
}
func (d *recordingDriver) Snapshot(context.Context, sandbox.ImageRef) (sandbox.SnapshotRef, error) {
	return sandbox.SnapshotRef{}, nil
}
func (d *recordingDriver) Run(_ context.Context, rs sandbox.RunSpec) (sandbox.RunResult, error) {
	d.mu.Lock()
	d.runs = append(d.runs, rs)
	hook := d.onRun
	d.mu.Unlock()
	if hook != nil {
		hook(rs)
	}
	return sandbox.RunResult{ExitCode: 0}, nil
}

func (d *recordingDriver) recordedRuns() []sandbox.RunSpec {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]sandbox.RunSpec, len(d.runs))
	copy(out, d.runs)
	return out
}

// harness wires a Store, a recordingDriver, a fakeAdapter, and a two-suite
// fixture (a five-task suite for the harness's default plan, a ten-task/
// 16-trigger suite for Full-tier tests) into runner.Options a test can run
// against.
type harness struct {
	t         *testing.T
	store     *store.Store
	driver    *recordingDriver
	adapter   *fakeAdapter
	adapters  func(name string) (agent.Adapter, bool)
	opts      runner.Options
	suite     *suite.Suite
	fullSuite *suite.Suite
	suiteDir  string
	bundleDir string
	image     sandbox.ImageRef
	evalID    int64
	skill     string
}

// newTaskSuite builds n tasks with distinct, prefixed ids so two fixtures
// can share one on-disk suite directory without colliding. holdoutN of them
// are spread across the id range as holdout, mirroring plan_test.go's
// fixture shape.
func newTaskSuite(prefix string, n, holdoutN int) *suite.Suite {
	s := &suite.Suite{}
	holdout := map[int]bool{}
	for i := 0; i < holdoutN; i++ {
		holdout[i*n/holdoutN] = true
	}
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("%s%02d", prefix, i)
		split := "dev"
		if holdout[i] {
			split = "holdout"
		}
		s.Tasks = append(s.Tasks, suite.TaskPkg{
			ID:       id,
			Kind:     "happy",
			Split:    split,
			PromptMD: "Do the thing for " + id + ".",
			Oracle:   "#!/bin/sh\ntrue\n",
			Verifier: "#!/bin/sh\nexit 0\n",
		})
	}
	return s
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	smoke := newTaskSuite("t", 5, 0)
	full := newTaskSuite("f", 10, 3)
	for i := 0; i < 8; i++ {
		full.Triggers.Positive = append(full.Triggers.Positive, fmt.Sprintf("please use the demo skill (%d)", i))
		full.Triggers.Negative = append(full.Triggers.Negative, fmt.Sprintf("do an unrelated thing (%d)", i))
	}

	// Both fixtures' tasks are written into one suite directory so a plan
	// built from either fixture resolves its task directories the same way.
	combined := &suite.Suite{Tasks: append(append([]suite.TaskPkg{}, smoke.Tasks...), full.Tasks...)}
	suiteDir := filepath.Join(t.TempDir(), "suite")
	if err := combined.Write(suiteDir); err != nil {
		t.Fatalf("writing suite fixture: %v", err)
	}

	bundleDir := filepath.Join(t.TempDir(), "bundle")
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		t.Fatalf("creating bundle dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "SKILL.md"), []byte("---\nname: demo\n---\nDo the thing.\n"), 0o644); err != nil {
		t.Fatalf("writing bundle SKILL.md: %v", err)
	}

	adapter := &fakeAdapter{stream: "raw-stream"}
	driver := &recordingDriver{isolated: false}
	adapters := func(name string) (agent.Adapter, bool) {
		if name == "claude-code" {
			return adapter, true
		}
		return nil, false
	}

	id, err := st.CreateEval(store.EvalRecord{
		Skill: "demo", SpecVersion: 1, Tier: "smoke", SuiteRef: "abc123", EngineVersion: "test", Seed: 1,
		Status: "running",
	})
	if err != nil {
		t.Fatalf("CreateEval: %v", err)
	}

	h := &harness{
		t: t, store: st, driver: driver, adapter: adapter, adapters: adapters,
		suite: smoke, fullSuite: full, suiteDir: suiteDir, bundleDir: bundleDir,
		image: sandbox.ImageRef{Tag: "demo:latest"}, evalID: id, skill: "demo",
	}
	h.opts = runner.Options{
		Store: st, Driver: driver, Adapters: adapters,
		Concurrency: 4, SessionTimeout: time.Minute,
		// No-op by default so tests that never touch rate limiting run at
		// full speed; the backoff test overrides this.
		Sleep: func(time.Duration) {},
	}
	return h
}

func (h *harness) options() runner.Options { return h.opts }

// smokePlan is the harness's default plan: every dev task in the five-task
// fixture, run once as skill and once as baseline on the panel's primary
// member. It is hand-built (not BuildPlan(TierSmoke, ...)) because the real
// smoke tier plans zero baselines, and several of these tests need a
// baseline to exist to have anything to check.
func (h *harness) smokePlan() *runner.Plan {
	panel := runner.DefaultPanel()
	primary := panel[0]
	var runs []store.RunKey
	for _, task := range h.suite.Tasks {
		runs = append(runs, store.RunKey{TaskID: task.ID, Agent: primary.Agent, Model: primary.Model, Condition: runner.CondSkill, Attempt: 1})
		runs = append(runs, store.RunKey{TaskID: task.ID, Agent: primary.Agent, Model: primary.Model, Condition: runner.CondBaseline, Attempt: 1})
	}
	return &runner.Plan{Tier: runner.TierSmoke, Panel: panel, Runs: runs, N: 1, K: 1, Tasks: h.suite.Tasks}
}

func (h *harness) input() runner.ExecuteInput {
	return runner.ExecuteInput{
		Skill:     h.skill,
		BundleDir: h.bundleDir,
		SuiteDir:  h.suiteDir,
		Image:     h.image,
		Plan:      h.smokePlan(),
	}
}

func (h *harness) run(ctx context.Context) (*runner.ExecuteResult, error) {
	h.t.Helper()
	r, err := runner.New(h.options())
	if err != nil {
		h.t.Fatalf("New: %v", err)
	}
	return r.Execute(ctx, h.evalID, h.input())
}

// workspaces returns the distinct session workspaces the driver saw while
// running a task's verifier — every skill and baseline run has exactly one.
func (h *harness) workspaces() []string {
	seen := map[string]bool{}
	var out []string
	for _, rs := range h.driver.recordedRuns() {
		if rs.Workspace != "" && !seen[rs.Workspace] {
			seen[rs.Workspace] = true
			out = append(out, rs.Workspace)
		}
	}
	return out
}

// probeWorkspaces returns the distinct workspaces used for trigger-probe
// sessions, identified by their prompt belonging to the full suite's trigger
// set (a probe session has no verifier, so it never appears in
// h.workspaces(), which only sees driver.Run calls).
func (h *harness) probeWorkspaces() []string {
	prompts := map[string]bool{}
	for _, p := range h.fullSuite.Triggers.Positive {
		prompts[p] = true
	}
	for _, p := range h.fullSuite.Triggers.Negative {
		prompts[p] = true
	}

	h.adapter.mu.Lock()
	defer h.adapter.mu.Unlock()
	seen := map[string]bool{}
	var out []string
	for _, s := range h.adapter.invokes {
		if prompts[s.Prompt] && !seen[s.Workspace] {
			seen[s.Workspace] = true
			out = append(out, s.Workspace)
		}
	}
	return out
}

// countInstalledSkills counts directories under <ws>/.claude/skills, the
// fixed skill directory fakeAdapter.Caps() reports.
func countInstalledSkills(t *testing.T, ws string) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(ws, ".claude", "skills"))
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatalf("reading skills dir: %v", err)
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() {
			n++
		}
	}
	return n
}

func mustDistractors(t *testing.T) []suite.Distractor {
	t.Helper()
	ds, err := suite.Distractors()
	if err != nil {
		t.Fatalf("suite.Distractors: %v", err)
	}
	return ds
}

func TestExecute_ResumeSpendsNoSessionOnACompletedRun(t *testing.T) {
	h := newHarness(t)
	first, err := h.run(context.Background())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(first.Outcomes) == 0 {
		t.Fatal("no outcomes")
	}
	before := h.adapter.invokeCount()

	second, err := h.run(context.Background())
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	// This is the whole point of checkpointing: an interrupted Full tier costs
	// what remains, not sixty sessions.
	if after := h.adapter.invokeCount(); after != before {
		t.Errorf("resume spent %d further sessions", after-before)
	}
	if len(second.Outcomes) != len(first.Outcomes) {
		t.Errorf("resume returned %d outcomes, want the %d already recorded", len(second.Outcomes), len(first.Outcomes))
	}
}

func TestExecute_BaselineNeverInstallsTheSkill(t *testing.T) {
	h := newHarness(t)
	res, err := h.run(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	skillRuns, baselineRuns := 0, 0
	for _, o := range res.Outcomes {
		switch o.Key.Condition {
		case runner.CondSkill:
			skillRuns++
		case runner.CondBaseline:
			baselineRuns++
		}
	}
	if baselineRuns == 0 {
		t.Fatal("no baseline runs planned; there is nothing to compare against")
	}
	// The baseline is the comparison. A baseline workspace carrying the skill
	// makes Uplift measure nothing, and every score built on it looks like a
	// skill with no effect.
	if got := h.adapter.installCount(); got != skillRuns {
		t.Errorf("%d installs for %d skill runs and %d baselines; the skill leaked into a baseline", got, skillRuns, baselineRuns)
	}
}

func TestExecute_RetriesARateLimitedSessionAfterBackingOff(t *testing.T) {
	h := newHarness(t)
	h.adapter.rateLimitFirst.Store(true)
	var slept []time.Duration
	h.opts.Sleep = func(d time.Duration) { slept = append(slept, d) }

	res, err := h.run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(slept) == 0 {
		t.Error("a rate-limited session was not backed off")
	}
	// A plan rate limit is a property of the account, not of the skill. Scoring
	// it as a failed session hands the skill a penalty it did not earn.
	for _, o := range res.Outcomes {
		if o.Status == "error" && strings.Contains(o.Err.Error(), "rate") {
			t.Errorf("rate-limited run recorded as an error: %+v", o)
		}
	}
}

func TestExecute_AFailingSessionDoesNotAbortTheEval(t *testing.T) {
	h := newHarness(t)
	var n atomic.Int32
	h.driver.onRun = func(sandbox.RunSpec) {
		if n.Add(1) == 1 {
			// one broken run, everything else fine
			h.adapter.invokeErr = errors.New("boom")
			go func() { time.Sleep(10 * time.Millisecond); h.adapter.invokeErr = nil }()
		}
	}

	res, err := h.run(context.Background())
	if err != nil {
		t.Fatalf("one failed session aborted the eval: %v", err)
	}
	// Fifty-nine usable sessions and one recorded failure is a result. Zero
	// sessions and an error is fifty-nine wasted.
	if len(res.Outcomes) == 0 {
		t.Error("no outcomes survived a single failed session")
	}
}

func TestExecute_NeverExceedsItsConcurrency(t *testing.T) {
	h := newHarness(t)
	h.opts.Concurrency = 3
	var live, peak atomic.Int32
	h.driver.onRun = func(sandbox.RunSpec) {
		cur := live.Add(1)
		for {
			p := peak.Load()
			if cur <= p || peak.CompareAndSwap(p, cur) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		live.Add(-1)
	}

	if _, err := h.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Concurrency is what keeps a subscription's rate limit and the host's
	// memory inside bounds. Exceeding it turns a slow eval into a failed one.
	if p := peak.Load(); p > 3 {
		t.Errorf("peak concurrency %d, want at most 3", p)
	}
}

func TestProbePanel_AnUnhealthyMemberMakesThePanelIncompleteRatherThanZero(t *testing.T) {
	h := newHarness(t)
	h.adapters = func(name string) (agent.Adapter, bool) {
		if name == "claude-code" {
			return h.adapter, true
		}
		return nil, false
	}
	panel := append(runner.DefaultPanel(), runner.Member{Agent: "cursor", Model: "auto"})

	r, err := runner.New(h.options())
	if err != nil {
		t.Fatal(err)
	}
	hs, err := r.ProbePanel(context.Background(), panel, sandbox.ImageRef{Tag: "t"})
	if err != nil {
		t.Fatal(err)
	}

	var unhealthy int
	for _, x := range hs {
		if !x.OK {
			unhealthy++
			if x.Detail == "" {
				t.Error("an unhealthy member reported no reason")
			}
		}
	}
	// An expired token or a churned CLI must not read as a skill that scores
	// zero on that member: min-across-panel would turn it into a publish block.
	if unhealthy != 1 {
		t.Errorf("%d unhealthy members, want 1", unhealthy)
	}

	in := h.input()
	in.Healthy = map[runner.Member]bool{}
	for _, x := range hs {
		in.Healthy[x.Member] = x.OK
	}
	in.Plan, _ = runner.BuildPlan(runner.TierSmoke, panel, h.suite, nil)
	res, err := r.Execute(context.Background(), h.evalID, in)
	if err != nil {
		t.Fatal(err)
	}
	if res.PanelComplete {
		t.Error("PanelComplete = true with an unhealthy member")
	}
	for _, o := range res.Outcomes {
		if o.Key.Agent == "cursor" {
			t.Errorf("ran a session on an unhealthy member: %+v", o.Key)
		}
	}
}

func TestExecute_TriggerProbesInstallTheDistractorPack(t *testing.T) {
	h := newHarness(t)
	in := h.input()
	in.Plan, _ = runner.BuildPlan(runner.TierFull, runner.DefaultPanel(), h.fullSuite, nil)
	in.Distractors = mustDistractors(t)

	r, err := runner.New(h.options())
	if err != nil {
		t.Fatal(err)
	}
	res, err := r.Execute(context.Background(), h.evalID, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Probes) == 0 {
		t.Fatal("no probes ran")
	}
	// Trigger precision measured against no distractors measures nothing: the
	// skill is the only candidate, so it always "wins".
	for _, ws := range h.probeWorkspaces() {
		if n := countInstalledSkills(t, ws); n < len(in.Distractors) {
			t.Errorf("probe workspace has %d skills, want the skill plus %d distractors", n, len(in.Distractors))
		}
	}
}

func TestExecute_NeverStagesTheOracleIntoASessionWorkspace(t *testing.T) {
	h := newHarness(t)
	if _, err := h.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, ws := range h.workspaces() {
		if _, err := os.Stat(filepath.Join(ws, "oracle", "solve.sh")); err == nil {
			t.Errorf("%s carries the oracle; the agent can read the reference solution", ws)
		}
	}
}
