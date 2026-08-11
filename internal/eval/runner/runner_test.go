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

// wsSnapshot is what fakeAdapter observed about a session's workspace at the
// moment it was invoked — the last point before the runner cleans the
// workspace up, and so the only reliable place left to check what the agent
// could actually see.
type wsSnapshot struct {
	prompt     string
	workspace  string
	skillCount int
	hasOracle  bool
	env        []string
}

// skillDirCount reports how many skill directories are present under
// ws/.claude/skills right now — the fixed skill directory fakeAdapter.Caps()
// reports.
func skillDirCount(ws string) int {
	entries, err := os.ReadDir(filepath.Join(ws, ".claude", "skills"))
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() {
			n++
		}
	}
	return n
}

// hasOracleFile reports whether ws carries the task's reference solution.
func hasOracleFile(ws string) bool {
	_, err := os.Stat(filepath.Join(ws, "oracle", "solve.sh"))
	return err == nil
}

// fakeAdapter records what it was asked to do and replays a canned stream.
type fakeAdapter struct {
	mu        sync.Mutex
	installs  []string
	invokes   []agent.InvokeSpec
	snapshots []wsSnapshot
	stream    string
	meta      agent.Meta
	invokeErr error
	// rateLimitFirst makes the first invocation report a rate limit, so the
	// backoff path is exercised without a real 429.
	rateLimitFirst atomic.Bool
	// alwaysRateLimited makes every invocation report a rate limit, so the
	// retry-exhaustion path is exercised without a real 429 that never lets up.
	alwaysRateLimited atomic.Bool
	// authEnv, when set, is returned from Caps().AuthEnv so a test can
	// exercise env-based credential forwarding without changing every other
	// fixture's Caps().
	authEnv []string
	// failOnce, when true, makes exactly the next Invoke call fail and then
	// clears itself atomically — CompareAndSwap rather than a plain bool field
	// so concurrent workers racing to invoke never see (or write) it
	// unsynchronized, and exactly one session fails regardless of scheduling,
	// not "whichever session happens to land inside a sleep window."
	failOnce atomic.Bool
}

func (f *fakeAdapter) Name() string { return "claude-code" }
func (f *fakeAdapter) Caps() agent.Caps {
	return agent.Caps{EventTier: "A", ModelFlag: "--model", SkillDir: ".claude/skills", AuthEnv: f.authEnv, SupportsSkillInvocation: true}
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
	// Snapshot the workspace now: it is staged and installed into by this
	// point, and the runner removes it once the session (and, for a task run,
	// its verifier) finishes — this is the only point a test can still see it.
	// InvokeSpec carries no workspace of its own (the sandbox already knows
	// it), so it comes from the executor the runner built around it.
	ws := ""
	var env []string
	if se, ok := s.Exec.(*sandbox.Executor); ok {
		ws = se.Workspace()
		env = se.Env()
	}
	f.snapshots = append(f.snapshots, wsSnapshot{
		prompt:     s.Prompt,
		workspace:  ws,
		skillCount: skillDirCount(ws),
		hasOracle:  hasOracleFile(ws),
		env:        env,
	})
	f.mu.Unlock()
	if f.failOnce.CompareAndSwap(true, false) {
		return nil, errors.New("boom")
	}
	if f.invokeErr != nil {
		return nil, f.invokeErr
	}
	return strings.NewReader(f.stream), nil
}
func (f *fakeAdapter) Parse(agent.RawStream) (*agent.Result, error) {
	m := f.meta
	if f.alwaysRateLimited.Load() || f.rateLimitFirst.CompareAndSwap(true, false) {
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

func (f *fakeAdapter) invokeSnapshots() []wsSnapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]wsSnapshot, len(f.snapshots))
	copy(out, f.snapshots)
	return out
}

// recordingDriver is a Driver that records every RunSpec it was asked to run
// and calls onRun, if set, synchronously from within Run — tests use it to
// observe or perturb concurrency and per-session failures.
type recordingDriver struct {
	mu       sync.Mutex
	isolated bool
	runs     []sandbox.RunSpec
	onRun    func(sandbox.RunSpec)
	// result, if set, overrides what Run returns — tests use it to simulate a
	// driver failure (a cancelled context, a docker client error) without a
	// real sandbox.
	result func(sandbox.RunSpec) (sandbox.RunResult, error)
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
	result := d.result
	d.mu.Unlock()
	if hook != nil {
		hook(rs)
	}
	if result != nil {
		return result(rs)
	}
	return sandbox.RunResult{ExitCode: 0}, nil
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
		// full speed; the backoff tests override this.
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

	// Not just the count: a resume that silently returned the wrong status or
	// exit code for a key would pass the checks above and still be wrong.
	want := map[store.RunKey]runner.Outcome{}
	for _, o := range first.Outcomes {
		want[o.Key] = o
	}
	for _, o := range second.Outcomes {
		w, ok := want[o.Key]
		if !ok {
			t.Errorf("resume reported %+v, which the first run never produced", o.Key)
			continue
		}
		if o.Status != w.Status || !sameVerifierExit(o.VerifierExit, w.VerifierExit) {
			t.Errorf("resume for %+v = {status=%s exit=%v}, want {status=%s exit=%v}",
				o.Key, o.Status, verifierExitStr(o.VerifierExit), w.Status, verifierExitStr(w.VerifierExit))
		}
	}
}

// sameVerifierExit compares two *int VerifierExit values by their pointed-to
// value (or by both being nil), never by pointer identity: two separately
// constructed Outcomes with "the verifier exited 0" must compare equal, and
// nil must never compare equal to a pointer to 0.
func sameVerifierExit(a, b *int) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	return a == nil || *a == *b
}

func verifierExitStr(v *int) string {
	if v == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%d", *v)
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

// TestExecute_ForwardsAuthEnvIntoTheSandboxRunSpec pins the fix for the
// broken credential path: a worker with no interactive login has none of
// AuthDirs' host directories, so ANTHROPIC_API_KEY set in the worker's own
// environment must reach the sandbox's RunSpec.Env, not just get logged as a
// skipped mount.
func TestExecute_ForwardsAuthEnvIntoTheSandboxRunSpec(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test-value")

	h := newHarness(t)
	h.adapter.authEnv = []string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN"}

	if _, err := h.run(context.Background()); err != nil {
		t.Fatal(err)
	}

	snapshots := h.adapter.invokeSnapshots()
	if len(snapshots) == 0 {
		t.Fatal("no sessions invoked")
	}
	for _, s := range snapshots {
		found := false
		for _, e := range s.env {
			if e == "CLAUDE_CODE_OAUTH_TOKEN=" || strings.HasPrefix(e, "CLAUDE_CODE_OAUTH_TOKEN=") {
				t.Errorf("unset CLAUDE_CODE_OAUTH_TOKEN was forwarded: %q", e)
			}
			if e == "ANTHROPIC_API_KEY=sk-ant-test-value" {
				found = true
			}
		}
		if !found {
			t.Errorf("session env = %v, want ANTHROPIC_API_KEY forwarded", s.env)
		}
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

func TestExecute_ExhaustingRateLimitRetriesIsRecordedAsAnErrorNotAResult(t *testing.T) {
	h := newHarness(t)
	h.adapter.alwaysRateLimited.Store(true)
	h.opts.MaxRateLimitRetries = 2

	res, err := h.run(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	var sawError bool
	for _, o := range res.Outcomes {
		// A rate-limit artifact that exhausted its retries must not be scored
		// via the verifier at all — once StatusFailed is written, ClaimRun
		// treats it as a completed measurement and it is never retried.
		if o.Status == store.StatusOK || o.Status == store.StatusFailed {
			t.Errorf("a permanently rate-limited session was scored as a completed measurement: %+v", o)
		}
		if o.Status == store.StatusError {
			sawError = true
		}
	}
	if !sawError {
		t.Fatal("no outcome recorded as an error for a permanently rate-limited session")
	}

	// Once the account stops rate-limiting, a resumed Execute must retry the
	// run rather than treating the earlier error as a completed measurement.
	h.adapter.alwaysRateLimited.Store(false)
	before := h.adapter.invokeCount()
	if _, err := h.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if after := h.adapter.invokeCount(); after == before {
		t.Error("resume did not retry a run recorded as an error")
	}
}

func TestExecute_AgentInternalErrorIsRecordedAsNotPerformed(t *testing.T) {
	h := newHarness(t)
	h.adapter.meta = agent.Meta{IsError: true}

	res, err := h.run(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	var sawError bool
	for _, o := range res.Outcomes {
		// Meta.IsError means the CLI reported an internal error even though the
		// transport succeeded — the session was not performed, and scoring it
		// via the verifier's exit code would read that as a skill failure.
		if o.Status == store.StatusOK || o.Status == store.StatusFailed {
			t.Errorf("an agent-reported internal error was scored as a completed measurement: %+v", o)
		}
		if o.Status == store.StatusError {
			sawError = true
		}
	}
	if !sawError {
		t.Fatal("no outcome recorded as an error for an agent-reported internal error")
	}
}

func TestExecute_ACancelledDriverRunIsRecordedAsErrorNotFailed(t *testing.T) {
	h := newHarness(t)
	// The verifier run (the only direct r.o.Driver.Run call executeRun makes)
	// reports a cancelled context, the shape docker.Driver.Run now returns
	// rather than folding a killed docker client's exit code into a
	// RunResult. A cancelled run is not a measurement — it must be recorded
	// as StatusError so a resume retries it, never as StatusFailed, which
	// ClaimRun treats as a completed, permanent skill failure.
	h.driver.result = func(sandbox.RunSpec) (sandbox.RunResult, error) {
		return sandbox.RunResult{Cancelled: true}, fmt.Errorf("docker: run cancelled: %w", context.Canceled)
	}

	res, err := h.run(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	var sawError bool
	for _, o := range res.Outcomes {
		if o.Status == store.StatusFailed {
			t.Errorf("a cancelled run was recorded as StatusFailed: %+v", o)
		}
		if o.Status == store.StatusError {
			sawError = true
		}
	}
	if !sawError {
		t.Fatal("no outcome recorded as an error for a cancelled driver run")
	}
}

func TestExecute_AFailingSessionDoesNotAbortTheEval(t *testing.T) {
	h := newHarness(t)
	// failOnce fails exactly the next Invoke call, atomically, so exactly one
	// session breaks regardless of how the runner schedules its concurrent
	// workers — not "whichever session happens to invoke inside a sleep
	// window," which is timing-dependent and races the same field against
	// every other concurrent Invoke.
	h.adapter.failOnce.Store(true)

	res, err := h.run(context.Background())
	if err != nil {
		t.Fatalf("one failed session aborted the eval: %v", err)
	}
	if len(res.Outcomes) == 0 {
		t.Error("no outcomes survived a single failed session")
	}
	var errored int
	for _, o := range res.Outcomes {
		if o.Status == store.StatusError {
			errored++
		}
	}
	if errored != 1 {
		t.Errorf("errored outcomes = %d, want exactly 1 (one broken session, everything else fine)", errored)
	}
}

func TestExecute_VerifierRunUsesSuitesTimeoutNotTheSessionTimeout(t *testing.T) {
	h := newHarness(t)
	// Deliberately different from suite.VerifierTimeout, so the two cannot
	// pass this test by coincidence.
	h.opts.SessionTimeout = 20 * time.Minute

	if _, err := h.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	// recordingDriver.Run is only ever called for the verifier (the agent
	// session itself goes through fakeAdapter.Invoke, which never touches the
	// driver) — every recorded run's Timeout must be suite.VerifierTimeout,
	// the same bound suite.Check uses for the identical script, not
	// whatever SessionTimeout the panel happens to be configured with.
	if len(h.driver.runs) == 0 {
		t.Fatal("no verifier runs were recorded")
	}
	for _, rs := range h.driver.runs {
		if rs.Timeout != suite.VerifierTimeout {
			t.Errorf("verifier run timeout = %s, want suite.VerifierTimeout (%s)", rs.Timeout, suite.VerifierTimeout)
		}
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
	panel := append(runner.DefaultPanel(), runner.Member{Agent: "unregistered-agent", Model: "auto"})

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
		if o.Key.Agent == "unregistered-agent" {
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

	prompts := map[string]bool{}
	for _, p := range h.fullSuite.Triggers.Positive {
		prompts[p] = true
	}
	for _, p := range h.fullSuite.Triggers.Negative {
		prompts[p] = true
	}

	var sawProbeSession bool
	for _, snap := range h.adapter.invokeSnapshots() {
		if !prompts[snap.prompt] {
			continue
		}
		sawProbeSession = true
		// Trigger precision measured against no distractors measures nothing:
		// the skill is the only candidate, so it always "wins". Checked at
		// invoke time, since the runner removes the workspace once the
		// session ends.
		if snap.skillCount < len(in.Distractors) {
			t.Errorf("probe workspace has %d skills, want the skill plus %d distractors", snap.skillCount, len(in.Distractors))
		}
	}
	if !sawProbeSession {
		t.Fatal("no probe sessions were observed")
	}
}

func TestExecute_ResumedProbeRecoversEventsFromArtifacts(t *testing.T) {
	h := newHarness(t)
	in := h.input()
	in.Plan, _ = runner.BuildPlan(runner.TierFull, runner.DefaultPanel(), h.fullSuite, nil)
	in.Distractors = mustDistractors(t)

	r, err := runner.New(h.options())
	if err != nil {
		t.Fatal(err)
	}
	first, err := r.Execute(context.Background(), h.evalID, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Probes) == 0 {
		t.Fatal("no probes ran")
	}
	for _, p := range first.Probes {
		if p.Unknown {
			t.Fatalf("probe %+v reported unknown on its first run: %s", p.Probe, p.Reason)
		}
		if len(p.Events) == 0 {
			t.Fatalf("probe %+v recorded no events on its first run", p.Probe)
		}
	}

	// A second Runner against the same eval resumes every probe from the
	// store rather than re-invoking the agent. Its evidence must come back
	// from events.jsonl, written where resumeProbeOutcome looks for it — not
	// as Unknown, which is what a missing or misformatted file degrades to.
	r2, err := runner.New(h.options())
	if err != nil {
		t.Fatal(err)
	}
	before := h.adapter.invokeCount()
	second, err := r2.Execute(context.Background(), h.evalID, in)
	if err != nil {
		t.Fatal(err)
	}
	if after := h.adapter.invokeCount(); after != before {
		t.Errorf("resume spent %d further probe sessions", after-before)
	}
	if len(second.Probes) != len(first.Probes) {
		t.Fatalf("resume returned %d probes, want the %d already recorded", len(second.Probes), len(first.Probes))
	}
	for _, p := range second.Probes {
		if p.Unknown {
			t.Errorf("resumed probe %+v came back unknown: %s", p.Probe, p.Reason)
		}
		if len(p.Events) == 0 {
			t.Errorf("resumed probe %+v recovered no events", p.Probe)
		}
	}
}

// targetRunKey is the first task's skill-condition run key on the harness's
// default panel — a fixed, single run the events-write-failure tests can
// locate deterministically among the whole plan's concurrent outcomes.
func (h *harness) targetRunKey() store.RunKey {
	primary := runner.DefaultPanel()[0]
	return store.RunKey{TaskID: h.suite.Tasks[0].ID, Agent: primary.Agent, Model: primary.Model, Condition: runner.CondSkill, Attempt: 1}
}

// outcomeFor finds the outcome for k among res.Outcomes, failing the test if
// it is absent.
func outcomeFor(t *testing.T, res *runner.ExecuteResult, k store.RunKey) runner.Outcome {
	t.Helper()
	for _, o := range res.Outcomes {
		if o.Key == k {
			return o
		}
	}
	t.Fatalf("no outcome for %+v", k)
	return runner.Outcome{}
}

func TestExecute_ResumeReloadsFullMetaFromGradingJSON(t *testing.T) {
	h := newHarness(t)
	h.adapter.meta = agent.Meta{
		AgentVersion: "2.1.220", InputTokens: 10, OutputTokens: 20,
		NumTurns: 5, VisibleSkills: []string{"demo"}, PermissionDenials: []string{"write /etc"},
	}
	k := h.targetRunKey()

	first, err := h.run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	fresh := outcomeFor(t, first, k)
	if fresh.MetaPartial {
		t.Fatalf("a fresh run's Meta reported partial: %s", fresh.MetaPartialReason)
	}

	second, err := h.run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	resumed := outcomeFor(t, second, k)
	// NumTurns, VisibleSkills, and PermissionDenials are not among the
	// store's own columns — they only survive resume if Meta is reloaded
	// from grading.json rather than rebuilt from the database row.
	if resumed.MetaPartial {
		t.Errorf("resumed Meta reported partial even though grading.json is present: %s", resumed.MetaPartialReason)
	}
	if resumed.Meta.NumTurns != 5 {
		t.Errorf("resumed Meta.NumTurns = %d, want 5 (lost on resume)", resumed.Meta.NumTurns)
	}
	if len(resumed.Meta.VisibleSkills) != 1 || resumed.Meta.VisibleSkills[0] != "demo" {
		t.Errorf("resumed Meta.VisibleSkills = %v, want [demo] (lost on resume)", resumed.Meta.VisibleSkills)
	}
	if len(resumed.Meta.PermissionDenials) != 1 {
		t.Errorf("resumed Meta.PermissionDenials = %v, lost on resume", resumed.Meta.PermissionDenials)
	}
}

func TestExecute_ResumeFallsBackToPartialMetaWhenGradingJSONIsUnavailable(t *testing.T) {
	h := newHarness(t)
	h.adapter.meta = agent.Meta{AgentVersion: "2.1.220", InputTokens: 10, NumTurns: 5}
	k := h.targetRunKey()

	first, err := h.run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	fresh := outcomeFor(t, first, k)

	// Simulate a lost or pre-artifact-writing eval: grading.json is gone, but
	// the row (and its five store columns) still is.
	if err := os.Remove(filepath.Join(fresh.ArtifactDir, "grading.json")); err != nil {
		t.Fatal(err)
	}

	second, err := h.run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	resumed := outcomeFor(t, second, k)
	// The honest answer when the full record cannot be recovered is a marked
	// partial Meta, not a silent zero that looks like the CLI reported none
	// of these fields.
	if !resumed.MetaPartial {
		t.Fatal("Meta rebuilt from store columns alone was not marked partial")
	}
	if resumed.MetaPartialReason == "" {
		t.Error("MetaPartial is true but MetaPartialReason is empty")
	}
	// What the five columns do cover must still come through.
	if resumed.Meta.AgentVersion != "2.1.220" || resumed.Meta.InputTokens != 10 {
		t.Errorf("partial Meta lost fields the store columns do cover: %+v", resumed.Meta)
	}
	// NumTurns is not one of the five columns; a partial Meta legitimately
	// zeroes it, which is exactly what MetaPartial exists to flag.
	if resumed.Meta.NumTurns != 0 {
		t.Errorf("partial Meta.NumTurns = %d, want 0 (not among the store's columns)", resumed.Meta.NumTurns)
	}
}

func TestExecute_EventsWriteFailureRecordsTheRunAsNotPerformed(t *testing.T) {
	h := newHarness(t)
	k := h.targetRunKey()

	artifactDir, err := h.store.RunDir(h.skill, h.evalID, k)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Pre-occupy events.jsonl with a directory so writing the trajectory
	// specifically fails while transcript.raw and grading.json — different
	// filenames — still succeed.
	if err := os.Mkdir(filepath.Join(artifactDir, "events.jsonl"), 0o755); err != nil {
		t.Fatal(err)
	}

	first, err := h.run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := outcomeFor(t, first, k)
	// Events are the only artifact scoring and resume read back. Recording
	// this as StatusOK would tell a later ClaimRun the run never needs
	// retrying, even though the one thing it produced that resume depends on
	// was never written.
	if got.Status != store.StatusError {
		t.Errorf("status = %q, want %q", got.Status, store.StatusError)
	}
	if _, err := os.ReadFile(filepath.Join(artifactDir, "transcript.raw")); err != nil {
		t.Errorf("transcript.raw was not written even though only events failed: %v", err)
	}

	before := h.adapter.invokeCount()
	second, err := h.run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if after := h.adapter.invokeCount(); after == before {
		t.Fatal("resume did not retry a run whose events could not be recorded")
	}
	resumed := outcomeFor(t, second, k)
	// The obstruction is still in place, so the retried attempt fails the
	// same way — which is the correct outcome, not a bug in the test: it
	// confirms the run keeps being treated as not-yet-performed rather than
	// being accepted once and then silently left broken.
	if resumed.Status != store.StatusError {
		t.Errorf("resumed status = %q, want %q (the obstruction was not removed)", resumed.Status, store.StatusError)
	}
}

func TestExecute_TranscriptWriteFailureStillRecordsACompletedMeasurement(t *testing.T) {
	h := newHarness(t)
	k := h.targetRunKey()

	artifactDir, err := h.store.RunDir(h.skill, h.evalID, k)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Pre-occupy transcript.raw with a directory so only the transcript write
	// fails; events.jsonl and grading.json still succeed.
	if err := os.Mkdir(filepath.Join(artifactDir, "transcript.raw"), 0o755); err != nil {
		t.Fatal(err)
	}

	res, err := h.run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := outcomeFor(t, res, k)
	// The transcript is secondary evidence a human digs into after the fact.
	// Losing it does not make the run unscoreable, so it must not be
	// downgraded to a retryable error the way an events failure is.
	if got.Status != store.StatusOK {
		t.Errorf("status = %q, want %q (a transcript failure must not block a completed measurement)", got.Status, store.StatusOK)
	}
	if _, err := os.ReadFile(filepath.Join(artifactDir, "events.jsonl")); err != nil {
		t.Errorf("events.jsonl was not written even though only the transcript failed: %v", err)
	}
}

func TestExecute_NeverStagesTheOracleIntoASessionWorkspace(t *testing.T) {
	h := newHarness(t)
	if _, err := h.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	snapshots := h.adapter.invokeSnapshots()
	if len(snapshots) == 0 {
		t.Fatal("no sessions ran")
	}
	for _, snap := range snapshots {
		if snap.hasOracle {
			t.Errorf("%s carries the oracle; the agent can read the reference solution", snap.workspace)
		}
	}
}

func TestExecute_RemovesTheWorkspaceOnceASessionFinishes(t *testing.T) {
	h := newHarness(t)
	if _, err := h.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	// A Full or Deep tier stages 60-100+ workspaces; nothing must be left
	// behind once each one's session (and, for a task run, its verifier) is
	// done.
	for _, snap := range h.adapter.invokeSnapshots() {
		if _, err := os.Stat(snap.workspace); !os.IsNotExist(err) {
			t.Errorf("workspace %s was not removed after its session finished (stat err = %v)", snap.workspace, err)
		}
	}
}
