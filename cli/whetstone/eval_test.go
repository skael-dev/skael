package whetstone_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	whetstone "github.com/skael-dev/skael/cli/whetstone"
	"github.com/skael-dev/skael/internal/eval/agent"
	"github.com/skael-dev/skael/internal/eval/contract"
	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/eval/runner"
	"github.com/skael-dev/skael/internal/eval/sandbox"
	"github.com/skael-dev/skael/internal/eval/score"
	"github.com/skael-dev/skael/internal/eval/spec"
	"github.com/skael-dev/skael/internal/eval/store"
	"github.com/skael-dev/skael/internal/eval/suite"
	"github.com/skael-dev/skael/internal/eval/trajectory"
	"github.com/skael-dev/skael/internal/ui"
)

func TestRunEvalWith_ProducesAReportCarryingItsProvenance(t *testing.T) {
	d, req := evalHarness(t)
	r, err := whetstone.RunEvalWith(context.Background(), d, req)
	if err != nil {
		t.Fatalf("RunEvalWith: %v", err)
	}
	if r.SuiteRef == "" {
		t.Error("report has no suite ref; no later score can be compared to it")
	}
	if len(r.ModelPanel) == 0 {
		t.Error("report has no model panel")
	}
	if r.EngineVersion == "" {
		t.Error("report has no engine version")
	}
	if r.Tier != string(runner.TierSmoke) {
		t.Errorf("tier = %q", r.Tier)
	}
}

func TestRunEvalWith_UntrustedRefusesASharedKernelDriver(t *testing.T) {
	d, req := evalHarness(t)
	req.Untrusted = true
	_, err := whetstone.RunEvalWith(context.Background(), d, req)
	if !errors.Is(err, sandbox.ErrUntrustedRequiresIsolation) {
		t.Fatalf("err = %v, want the untrusted gate to refuse", err)
	}
	// The message has to say what to do. "Refused" without "because Docker
	// shares the host kernel" is a dead end.
	if !strings.Contains(err.Error(), "docker") {
		t.Errorf("err = %v, want it to name the driver", err)
	}
}

func TestRunEvalWith_StopsWhenTheSuiteHasVoidTasks(t *testing.T) {
	d, req := evalHarness(t)
	req.Skill = "voided" // harness seeds a stored suite check with one void task
	_, err := whetstone.RunEvalWith(context.Background(), d, req)
	if err == nil {
		t.Fatal("eval ran against a suite with a void task and no --allow-void")
	}
	if !strings.Contains(err.Error(), "suite check") {
		t.Errorf("err = %v, want it to point at `whetstone suite check`", err)
	}

	req.AllowVoid = true
	if _, err := whetstone.RunEvalWith(context.Background(), d, req); err != nil {
		t.Errorf("--allow-void did not proceed: %v", err)
	}
}

func TestRunEvalWith_ResumeReusesTheStoredEval(t *testing.T) {
	d, req := evalHarness(t)
	first, err := whetstone.RunEvalWith(context.Background(), d, req)
	if err != nil {
		t.Fatal(err)
	}
	id, err := d.Store.LatestEval(req.Skill)
	if err != nil {
		t.Fatal(err)
	}

	req.Resume = id.ID
	second, err := whetstone.RunEvalWith(context.Background(), d, req)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	// Resume must produce the same score, not a second eval whose sessions were
	// all cache hits but whose identity is new — a new eval id breaks every
	// trend that referenced the first.
	if second.Headline != first.Headline {
		t.Errorf("resumed headline %v, want %v", second.Headline, first.Headline)
	}
	if n := countEvals(t, d.Store, req.Skill); n != 1 {
		t.Errorf("%d eval rows after a resume, want 1", n)
	}
}

func TestRunEvalWith_MarksTheFallbackWhenNoCalibrationIsPossible(t *testing.T) {
	d, req := evalHarness(t)
	d.Gateway = nil // no gateway: no judge, no κ
	r, err := whetstone.RunEvalWith(context.Background(), d, req)
	if err != nil {
		t.Fatalf("eval with no gateway failed outright: %v", err)
	}
	// A missing gateway degrades Uplift; it does not invalidate the
	// deterministic pillars. Reporting nothing here would throw away a usable
	// Reliability measurement.
	if r.UpliftSource != score.UpliftPassRate {
		t.Errorf("UpliftSource = %q, want the fallback", r.UpliftSource)
	}
	// A test that only checks UpliftSource passes against an implementation
	// that fabricates κ=0 alongside a "judge" source label just as easily as
	// against a correct one — with no gateway at all there must be no κ on
	// the report, distinguishable from a judge that was calibrated and
	// scored exactly 0.
	if r.JudgeKappa != nil {
		t.Errorf("JudgeKappa = %v, want nil with no gateway available", *r.JudgeKappa)
	}
}

func TestRunEvalWith_RecordsJudgeKappaWhenAGatewayIsAvailable(t *testing.T) {
	d, req := evalHarness(t)
	r, err := whetstone.RunEvalWith(context.Background(), d, req)
	if err != nil {
		t.Fatalf("RunEvalWith: %v", err)
	}
	if r.JudgeKappa == nil {
		t.Fatal("JudgeKappa is nil with a gateway available; calibration should have run")
	}
}

// evalHarnessFull is evalHarness's Full-tier counterpart: a 10-task suite
// with a dev/holdout split and 8 positive/8 negative trigger phrases, so
// BuildPlan can actually fill a Full tier's budget (baselines and probes),
// unlike evalHarness's Smoke-tier fixture which plans neither. Nothing here
// exercises Reliability, Uplift-from-baseline, or the trigger-probe path
// today — this is what closes that hole.
func evalHarnessFull(t *testing.T) (whetstone.EvalDeps, whetstone.EvalRequest) {
	t.Helper()

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	seedFullSkill(t, st, "demo")

	adapter := &fakeAdapter{}
	adapters := func(name string) (agent.Adapter, bool) {
		if name == "claude-code" {
			return adapter, true
		}
		return nil, false
	}

	d := whetstone.EvalDeps{
		Store:         st,
		Driver:        &fakeDriver{},
		Gateway:       newScriptedGateway(t),
		Adapters:      adapters,
		Now:           func() time.Time { return time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC) },
		Sleep:         func(time.Duration) {},
		EngineVersion: "test-engine",
	}
	req := whetstone.EvalRequest{Skill: "demo", Tier: runner.TierFull}
	return d, req
}

// seedFullSkill is seedSkill's Full-tier counterpart: 10 tasks split 7
// dev/3 holdout, plus 8 positive and 8 negative trigger phrases — the
// minimum a Full tier's BuildPlan needs to fill its budget (10 tasks, 16
// probes) without refusing.
func seedFullSkill(t *testing.T, st *store.Store, name string) {
	t.Helper()

	sp := &spec.SkillSpec{
		Name:        name,
		Purpose:     "Do the thing.",
		Description: "Does the thing, for testing.",
		Triggers:    []spec.TriggerPhrase{{Text: "do the thing"}},
		Steps: []spec.Step{
			{ID: "s1", Action: "Run scripts/do.py", Postcondition: "out/done.txt exists"},
		},
		TargetTier: spec.TierMid,
	}
	if errs := sp.Validate(); len(errs) > 0 {
		t.Fatalf("invalid fixture spec: %v", errs)
	}
	version, err := st.SaveSpec(sp)
	if err != nil {
		t.Fatalf("SaveSpec: %v", err)
	}
	if err := st.ApproveSpec(name, version); err != nil {
		t.Fatalf("ApproveSpec: %v", err)
	}

	c, err := contract.Compile(sp)
	if err != nil {
		t.Fatalf("contract.Compile: %v", err)
	}
	contractPath, err := st.ContractPath(name)
	if err != nil {
		t.Fatalf("ContractPath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(contractPath), 0o755); err != nil {
		t.Fatal(err)
	}
	cf, err := os.Create(contractPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Save(cf); err != nil {
		t.Fatal(err)
	}
	if err := cf.Close(); err != nil {
		t.Fatal(err)
	}

	s := &suite.Suite{}
	for i := 0; i < 10; i++ {
		s.Tasks = append(s.Tasks, suite.TaskPkg{
			ID:       fmt.Sprintf("t%02d", i),
			Kind:     "happy",
			PromptMD: fmt.Sprintf("Do the thing for task %d.", i),
			Oracle:   "#!/bin/sh\ntrue\n",
			Verifier: "#!/bin/sh\nexit 0\n",
		})
	}
	s.Split(99)
	for i := 0; i < 8; i++ {
		s.Triggers.Positive = append(s.Triggers.Positive, fmt.Sprintf("please do the thing %d", i))
		s.Triggers.Negative = append(s.Triggers.Negative, fmt.Sprintf("do an adjacent thing %d", i))
	}

	suiteDir, err := st.SuiteDir(name)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Write(suiteDir); err != nil {
		t.Fatalf("writing suite fixture: %v", err)
	}
	ref, err := suite.Ref(suiteDir)
	if err != nil {
		t.Fatal(err)
	}

	rows := make([]store.SuiteCheckRow, len(s.Tasks))
	for i, task := range s.Tasks {
		rows[i] = store.SuiteCheckRow{TaskID: task.ID}
	}
	if err := st.SaveSuiteCheck(name, ref, rows); err != nil {
		t.Fatalf("SaveSuiteCheck: %v", err)
	}

	bundleDir, err := st.SkillDir(name)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "SKILL.md"), []byte("---\nname: "+name+"\n---\nDo the thing.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRunEvalWith_FullTierScoresBaselineAtItsOwnK(t *testing.T) {
	d, req := evalHarnessFull(t)
	r, err := whetstone.RunEvalWith(context.Background(), d, req)
	if err != nil {
		t.Fatalf("RunEvalWith: %v", err)
	}
	if len(r.Members) == 0 {
		t.Fatal("no members scored")
	}
	// This is the regression test for the bug where the baseline condition
	// was scored with the skill's own K (2 for Full) against BaselineRuns=1,
	// score.PassAtK refused with k > n, the refusal was swallowed, and
	// baselinePassRate silently stayed equal to reliability — pinning
	// UpliftFromPassRates(r, r) at exactly 0.5 on every Full/Deep eval. With
	// every skill and baseline run succeeding deterministically here,
	// reliability and baselinePassRate should both land near 1, so Uplift
	// from a real (non-degenerate) comparison should also land near 0.5 —
	// but critically, it must be reachable via a real baseline pass rate,
	// not a swallowed error. Assert the eval did not abort in the process,
	// which is what a k>n refusal being surfaced instead of swallowed would
	// have done before the fix (see the errored-run test for a case where a
	// value other than a constant 0.5 makes the difference visible).
	for _, mr := range r.Members {
		if !mr.Healthy {
			continue
		}
		if mr.Pillars.Reliability != 1 {
			t.Errorf("member %s/%s Reliability = %v, want 1 (every skill run succeeds in this fixture)", mr.Member.Agent, mr.Member.Model, mr.Pillars.Reliability)
		}
		if mr.Pillars.Uplift < 0 || mr.Pillars.Uplift > 1 {
			t.Errorf("member %s/%s Uplift = %v, out of range", mr.Member.Agent, mr.Member.Model, mr.Pillars.Uplift)
		}
	}
	// newScriptedGateway answers the real embedded calibration set correctly
	// (see its doc), so calibration clears the κ floor here and every scored
	// member's Uplift comes from the judge — this is the end-to-end judge
	// path the coverage hole named: with a trusted judge and baselines
	// present, the report must say so rather than silently reading as the
	// pass-rate fallback.
	if r.UpliftSource != score.UpliftJudge {
		t.Errorf("UpliftSource = %q, want %q end to end with a trusted judge and baselines present", r.UpliftSource, score.UpliftJudge)
	}
}

func TestRunEvalWith_PopulatesTaskAndUnevaluableCarriers(t *testing.T) {
	d, req := evalHarnessFull(t)
	r, err := whetstone.RunEvalWith(context.Background(), d, req)
	if err != nil {
		t.Fatalf("RunEvalWith: %v", err)
	}
	// report.Tasks/Unevaluable/UnevaluableDetail were left permanently empty
	// before the fix, which dark-out several live HTML sections regardless
	// of what the eval actually measured.
	if len(r.Tasks) == 0 {
		t.Fatal("report has no per-task detail")
	}
	for _, tr := range r.Tasks {
		if len(tr.Conditions) == 0 {
			t.Errorf("task %s has no condition detail", tr.TaskID)
		}
		if len(tr.Drift) == 0 {
			t.Errorf("task %s has no per-run drift detail", tr.TaskID)
		}
	}
}

func TestRunEvalWith_ResumeRefusesAnEvalForADifferentSkill(t *testing.T) {
	d, req := evalHarness(t)
	if _, err := whetstone.RunEvalWith(context.Background(), d, req); err != nil {
		t.Fatal(err)
	}
	id, err := d.Store.LatestEval(req.Skill)
	if err != nil {
		t.Fatal(err)
	}

	other := req
	other.Skill = "voided"
	other.AllowVoid = true
	other.Resume = id.ID
	_, err = whetstone.RunEvalWith(context.Background(), d, other)
	if err == nil {
		t.Fatal("resume across skills did not refuse")
	}
	if !strings.Contains(err.Error(), "not") {
		t.Errorf("err = %v, want it to name the skill mismatch", err)
	}
}

func TestRunEvalWith_ResumeRefusesAChangedSuiteRef(t *testing.T) {
	d, req := evalHarness(t)
	if _, err := whetstone.RunEvalWith(context.Background(), d, req); err != nil {
		t.Fatal(err)
	}
	id, err := d.Store.LatestEval(req.Skill)
	if err != nil {
		t.Fatal(err)
	}

	// Regenerate the suite so its content ref changes, but keep a suite
	// check recorded for the new ref so the eval otherwise proceeds.
	seedSkill(t, d.Store, req.Skill, 6, nil)

	req.Resume = id.ID
	_, err = whetstone.RunEvalWith(context.Background(), d, req)
	if err == nil {
		t.Fatal("resume across a changed suite did not refuse")
	}
	if !strings.Contains(err.Error(), "suite") {
		t.Errorf("err = %v, want it to name the suite mismatch", err)
	}
}

func TestRunEvalWith_ResumeRefusesAChangedPanel(t *testing.T) {
	d, req := evalHarness(t)
	if _, err := whetstone.RunEvalWith(context.Background(), d, req); err != nil {
		t.Fatal(err)
	}
	id, err := d.Store.LatestEval(req.Skill)
	if err != nil {
		t.Fatal(err)
	}

	req.Resume = id.ID
	req.Agents = []string{"claude-code", "claude-code"}
	req.Models = []string{"opus", "haiku"}
	_, err = whetstone.RunEvalWith(context.Background(), d, req)
	if err == nil {
		t.Fatal("resume across a changed panel did not refuse")
	}
	if !strings.Contains(err.Error(), "panel") {
		t.Errorf("err = %v, want it to name the panel mismatch", err)
	}
}

func TestRunEvalWith_ResumeKeepsTheOriginalStartedAt(t *testing.T) {
	d, req := evalHarness(t)
	first, err := whetstone.RunEvalWith(context.Background(), d, req)
	if err != nil {
		t.Fatal(err)
	}
	id, err := d.Store.LatestEval(req.Skill)
	if err != nil {
		t.Fatal(err)
	}

	d.Now = func() time.Time { return time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC) }
	req.Resume = id.ID
	second, err := whetstone.RunEvalWith(context.Background(), d, req)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if !second.StartedAt.Equal(first.StartedAt) {
		t.Errorf("resumed StartedAt = %v, want the original %v, not the resume's own now()", second.StartedAt, first.StartedAt)
	}
}

// errorOnceAdapter wraps fakeAdapter and fails the first Invoke whose prompt
// matches. A single failing session must degrade that one task's Reliability
// estimate, not abort the eval — see TestRunEvalWith_AnErroredRunDoesNotAbortTheEval.
type errorOnceAdapter struct {
	fakeAdapter
	match string
	fired bool
}

func (a *errorOnceAdapter) Invoke(ctx context.Context, spec agent.InvokeSpec) (agent.RawStream, error) {
	if !a.fired && strings.Contains(spec.Prompt, a.match) {
		a.fired = true
		return nil, errors.New("errorOnceAdapter: simulated infrastructure failure")
	}
	return a.fakeAdapter.Invoke(ctx, spec)
}

func TestRunEvalWith_AnErroredRunDoesNotAbortTheEval(t *testing.T) {
	d, req := evalHarnessFull(t)
	adapter := &errorOnceAdapter{match: "task 0."}
	d.Adapters = func(name string) (agent.Adapter, bool) {
		if name == "claude-code" {
			return adapter, true
		}
		return nil, false
	}

	r, err := whetstone.RunEvalWith(context.Background(), d, req)
	if err != nil {
		t.Fatalf("one errored session aborted the whole eval: %v", err)
	}
	if !adapter.fired {
		t.Fatal("the fixture never actually exercised the errored-run path")
	}
	if len(r.Members) == 0 {
		t.Fatal("no members scored")
	}
}

func TestRunEvalWith_JSONOutputCarriesTheRequiredKeys(t *testing.T) {
	d, req := evalHarness(t)
	ui.JSONMode = true
	defer func() { ui.JSONMode = false }()

	old := os.Stdout
	rf, wf, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = wf
	_, runErr := whetstone.RunEvalWith(context.Background(), d, req)
	_ = wf.Close()
	os.Stdout = old
	if runErr != nil {
		t.Fatalf("RunEvalWith: %v", runErr)
	}

	out, err := io.ReadAll(rf)
	if err != nil {
		t.Fatal(err)
	}

	// ui.PrintJSON indents its output across multiple lines, so the object
	// itself — not any single line of it — is what --json's contract is
	// about; a caller decodes the whole stream as one value.
	var obj map[string]any
	if err := json.Unmarshal(out, &obj); err != nil {
		t.Fatalf("--json output is not a single JSON object: %v\n%s", err, out)
	}
	for _, key := range []string{"eval_id", "skill", "tier", "suite_ref", "headline", "panel_complete", "uplift_source"} {
		if _, ok := obj[key]; !ok {
			t.Errorf("--json output missing required key %q: %v", key, obj)
		}
	}
}

// countEvals counts how many eval rows exist for skill, by walking ids
// upward from the latest one recorded — the store exposes no plain "list all
// evals" query, and this test only needs a count, not a listing.
func countEvals(t *testing.T, st *store.Store, skill string) int {
	t.Helper()
	latest, err := st.LatestEval(skill)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for id := int64(1); id <= latest.ID; id++ {
		if e, err := st.Eval(id); err == nil && e.Skill == skill {
			n++
		}
	}
	return n
}

// evalHarness builds a temp workspace with two skills seeded (demo, voided),
// an approved spec, a written suite plus its stored suite_checks rows, a fake
// driver, a fake adapter, and a scripted gateway, returning
// (EvalDeps, EvalRequest{Skill: "demo", Tier: runner.TierSmoke}).
func evalHarness(t *testing.T) (whetstone.EvalDeps, whetstone.EvalRequest) {
	t.Helper()

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	seedSkill(t, st, "demo", 5, nil)
	seedSkill(t, st, "voided", 6, map[int]string{0: "the oracle failed"})

	adapter := &fakeAdapter{}
	adapters := func(name string) (agent.Adapter, bool) {
		if name == "claude-code" {
			return adapter, true
		}
		return nil, false
	}

	d := whetstone.EvalDeps{
		Store:         st,
		Driver:        &fakeDriver{},
		Gateway:       newScriptedGateway(t),
		Adapters:      adapters,
		Now:           func() time.Time { return time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC) },
		Sleep:         func(time.Duration) {},
		EngineVersion: "test-engine",
	}
	req := whetstone.EvalRequest{Skill: "demo", Tier: runner.TierSmoke}
	return d, req
}

// seedSkill writes an approved spec, its compiled contract, and an n-task
// suite (with stored suite_checks rows) for name. voidTasks maps a task
// index to the void reason it should be recorded with.
func seedSkill(t *testing.T, st *store.Store, name string, n int, voidTasks map[int]string) {
	t.Helper()

	sp := &spec.SkillSpec{
		Name:        name,
		Purpose:     "Do the thing.",
		Description: "Does the thing, for testing.",
		Triggers:    []spec.TriggerPhrase{{Text: "do the thing"}},
		Steps: []spec.Step{
			{ID: "s1", Action: "Run scripts/do.py", Postcondition: "out/done.txt exists"},
		},
		TargetTier: spec.TierMid,
	}
	if errs := sp.Validate(); len(errs) > 0 {
		t.Fatalf("invalid fixture spec: %v", errs)
	}
	version, err := st.SaveSpec(sp)
	if err != nil {
		t.Fatalf("SaveSpec: %v", err)
	}
	if err := st.ApproveSpec(name, version); err != nil {
		t.Fatalf("ApproveSpec: %v", err)
	}

	c, err := contract.Compile(sp)
	if err != nil {
		t.Fatalf("contract.Compile: %v", err)
	}
	contractPath, err := st.ContractPath(name)
	if err != nil {
		t.Fatalf("ContractPath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(contractPath), 0o755); err != nil {
		t.Fatal(err)
	}
	cf, err := os.Create(contractPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Save(cf); err != nil {
		t.Fatal(err)
	}
	if err := cf.Close(); err != nil {
		t.Fatal(err)
	}

	s := &suite.Suite{}
	for i := 0; i < n; i++ {
		s.Tasks = append(s.Tasks, suite.TaskPkg{
			ID:       fmt.Sprintf("t%02d", i),
			Kind:     "happy",
			PromptMD: fmt.Sprintf("Do the thing for task %d.", i),
			Oracle:   "#!/bin/sh\ntrue\n",
			Verifier: "#!/bin/sh\nexit 0\n",
		})
	}
	suiteDir, err := st.SuiteDir(name)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Write(suiteDir); err != nil {
		t.Fatalf("writing suite fixture: %v", err)
	}
	ref, err := suite.Ref(suiteDir)
	if err != nil {
		t.Fatal(err)
	}

	rows := make([]store.SuiteCheckRow, len(s.Tasks))
	for i, task := range s.Tasks {
		rows[i] = store.SuiteCheckRow{TaskID: task.ID}
		if reason, void := voidTasks[i]; void {
			rows[i].Void = true
			rows[i].Reason = reason
		}
	}
	if err := st.SaveSuiteCheck(name, ref, rows); err != nil {
		t.Fatalf("SaveSuiteCheck: %v", err)
	}

	bundleDir, err := st.SkillDir(name)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "SKILL.md"), []byte("---\nname: "+name+"\n---\nDo the thing.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fakeDriver is a sandbox.Driver that shares the host kernel (like Docker)
// and always reports success — the suite's oracle/verifier scripts this
// eval never actually inspects.
type fakeDriver struct{}

func (fakeDriver) Name() string           { return "docker" }
func (fakeDriver) HardwareIsolated() bool { return false }
func (fakeDriver) Prepare(context.Context, sandbox.EnvSpec) (sandbox.ImageRef, error) {
	return sandbox.ImageRef{Tag: "demo:latest"}, nil
}
func (fakeDriver) Snapshot(context.Context, sandbox.ImageRef) (sandbox.SnapshotRef, error) {
	return sandbox.SnapshotRef{}, nil
}
func (fakeDriver) Run(context.Context, sandbox.RunSpec) (sandbox.RunResult, error) {
	return sandbox.RunResult{ExitCode: 0}, nil
}

// fakeAdapter is an agent.Adapter that reports a step matching the fixture
// contract's one required step, so drift scores deterministically.
type fakeAdapter struct{}

func (fakeAdapter) Name() string { return "claude-code" }
func (fakeAdapter) Caps() agent.Caps {
	return agent.Caps{EventTier: "A", ModelFlag: "--model", SkillDir: ".claude/skills", SupportsSkillInvocation: true}
}
func (fakeAdapter) InstallSkill(string, string) error { return nil }
func (fakeAdapter) Invoke(context.Context, agent.InvokeSpec) (agent.RawStream, error) {
	return strings.NewReader("raw-stream: ran scripts/do.py and wrote out/done.txt"), nil
}
func (fakeAdapter) Parse(agent.RawStream) (*agent.Result, error) {
	return &agent.Result{
		Events: []trajectory.Event{{Seq: 1, Type: trajectory.TypeShell, Name: "bash scripts/do.py"}},
		Meta:   agent.Meta{InputTokens: 100, OutputTokens: 50},
	}, nil
}

// scriptedGateway answers every judge call deterministically, quoting a
// verbatim substring pulled straight out of the prompt's own candidate
// section — so the evidence check always passes, for both the real embedded
// calibration set (score.Calibrate) and any per-task pairwise comparison,
// with no network and no knowledge of their content baked in.
//
// For a pairwise call whose candidate text matches one of the embedded
// calibration items (calLabels), it answers with that item's own human
// label — "skill", "baseline", or "tie" pass resolveWinner unchanged
// regardless of which position the candidate was shown in, so both of
// Judge.Pairwise's orderings agree and κ is computable at all. Without this
// a gateway that always says "tie" scores κ=0 against the calibration set's
// mixed labels and every eval permanently falls back to the pass-rate
// estimator, which is a fixture bug, not the behavior under test. A
// candidate with no match (every real per-task comparison the eval itself
// runs, as opposed to calibration) falls back to a flat "tie": the eval only
// asserts Uplift lands in range, not a specific value.
type scriptedGateway struct {
	calLabels map[string]string
}

// newScriptedGateway builds a scriptedGateway that can answer the embedded
// calibration set correctly, keyed by each item's own skill/baseline
// transcript text.
func newScriptedGateway(t *testing.T) scriptedGateway {
	t.Helper()
	set, err := score.Calibration()
	if err != nil {
		t.Fatalf("score.Calibration: %v", err)
	}
	m := make(map[string]string, len(set.Items)*2)
	for _, it := range set.Items {
		m[strings.TrimSpace(it.Skill)] = it.Label
		m[strings.TrimSpace(it.Baseline)] = it.Label
	}
	return scriptedGateway{calLabels: m}
}

func (g scriptedGateway) Complete(_ context.Context, r llm.Req) (llm.Res, error) {
	switch r.Role {
	case "score.pairwise":
		a := strings.TrimSpace(extractSection(r.Prompt, "Candidate A:\n", "\n\nCandidate B:"))
		if label, ok := g.calLabels[a]; ok {
			margin := 0.0
			if label != "tie" {
				margin = 0.8
			}
			return jsonResponse(map[string]any{"winner": label, "margin": margin, "evidence": []string{shortQuote(a)}})
		}
		q := shortQuote(a)
		return jsonResponse(map[string]any{"winner": "tie", "margin": 0, "evidence": []string{q}})
	case "score.semantic":
		q := shortQuote(extractSection(r.Prompt, "Transcript:\n", "\n\nReply with JSON only:"))
		return jsonResponse(map[string]any{"satisfied": true, "confidence": 1, "evidence": []string{q}})
	default:
		return llm.Res{}, fmt.Errorf("scriptedGateway: no script for role %q", r.Role)
	}
}

func extractSection(s, start, end string) string {
	i := strings.Index(s, start)
	if i < 0 {
		return ""
	}
	rest := s[i+len(start):]
	if j := strings.Index(rest, end); j >= 0 {
		return rest[:j]
	}
	return rest
}

// shortQuote trims s to a word boundary within the first 60 characters, so
// it stays a genuine, checkable substring of the transcript it came from.
func shortQuote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= 60 {
		return s
	}
	if cut := strings.LastIndexAny(s[:60], " \n\t"); cut > 0 {
		return s[:cut]
	}
	return s[:60]
}

func jsonResponse(v any) (llm.Res, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return llm.Res{}, err
	}
	return llm.Res{Text: string(b), Model: "scripted"}, nil
}
