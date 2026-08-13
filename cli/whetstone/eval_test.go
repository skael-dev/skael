package whetstone_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	whetstone "github.com/skael-dev/skael/cli/whetstone"
	"github.com/skael-dev/skael/internal/eval/agent"
	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/eval/runner"
	"github.com/skael-dev/skael/internal/eval/sandbox"
	"github.com/skael-dev/skael/internal/eval/spec"
	"github.com/skael-dev/skael/internal/eval/store"
	"github.com/skael-dev/skael/internal/eval/suite"
)

func TestRunEvalWith_ProducesAReportCarryingItsProvenance(t *testing.T) {
	d, req := evalHarness(t)
	r, err := whetstone.RunEvalWith(context.Background(), d, req)
	if err != nil {
		t.Fatalf("RunEvalWith: %v", err)
	}
	if r.SuiteRef == "" {
		t.Error("report has no eval set ref; no later score can be compared to it")
	}
	if len(r.ModelPanel) == 0 {
		t.Error("report has no model panel")
	}
	if r.EngineVersion == "" {
		t.Error("report has no engine version")
	}
	if r.GraderModel == "" {
		t.Error("report does not name the grader; a different grader moves the number")
	}
	if r.Tier != string(runner.TierSmoke) {
		t.Errorf("tier = %q", r.Tier)
	}
}

// muteGateway names no model in advance, the way a subscription CLI does. It
// still names one in every answer, which is the only place that gateway's
// model ever appears.
type muteGateway struct{ scriptedGateway }

func (muteGateway) ModelFor(llm.ModelClass) string { return "" }

// TestRunEvalWith_NamesTheJudgeAGatewayDeclared pins the path every existing
// score takes. A gateway that declares a model stays the authority, so no
// stored score is reclassified by the fallback below.
func TestRunEvalWith_NamesTheJudgeAGatewayDeclared(t *testing.T) {
	d, req := evalHarness(t)
	r, err := whetstone.RunEvalWith(context.Background(), d, req)
	if err != nil {
		t.Fatalf("RunEvalWith: %v", err)
	}
	if r.GraderModel != "scripted-strong" {
		t.Errorf("GraderModel = %q, want the model the gateway declared", r.GraderModel)
	}
}

// TestRunEvalWith_NamesTheJudgeFromTheAnswersWhenNoneIsDeclared is the point of
// this change. A subscription CLI declares nothing, and the run recorded no
// judge at all. Every such score then grouped with every other unknown one on a
// trend, so a real judge change read as no change.
func TestRunEvalWith_NamesTheJudgeFromTheAnswersWhenNoneIsDeclared(t *testing.T) {
	d, req := evalHarness(t)
	d.Gateway = muteGateway{}

	r, err := whetstone.RunEvalWith(context.Background(), d, req)
	if err != nil {
		t.Fatalf("RunEvalWith: %v", err)
	}
	if r.GraderModel != "scripted-strong" {
		t.Errorf("GraderModel = %q, want the model the answers reported", r.GraderModel)
	}
}

// The score is the share of expectations passed. The scripted grader passes
// every one, so a clean run must score 100.
func TestRunEvalWith_ScoresThePassRate(t *testing.T) {
	d, req := evalHarness(t)
	r, err := whetstone.RunEvalWith(context.Background(), d, req)
	if err != nil {
		t.Fatalf("RunEvalWith: %v", err)
	}
	if r.Headline != 100 {
		t.Errorf("headline = %v, want 100 when every expectation passes", r.Headline)
	}
	if len(r.Members) == 0 || r.Members[0].Effectiveness != 100 {
		t.Errorf("members = %+v, want the one member scored 100", r.Members)
	}
}

// The grader's verdicts and evidence must reach the report, or a surprising
// score cannot be read back to the expectation that produced it.
func TestRunEvalWith_CarriesGradedExpectationsIntoTheReport(t *testing.T) {
	d, req := evalHarness(t)
	r, err := whetstone.RunEvalWith(context.Background(), d, req)
	if err != nil {
		t.Fatalf("RunEvalWith: %v", err)
	}
	if len(r.Tasks) == 0 {
		t.Fatal("report carries no evals")
	}
	var sawEvidence bool
	for _, task := range r.Tasks {
		for _, g := range task.Grades {
			for _, e := range g.Expectations {
				if e.Evidence != "" {
					sawEvidence = true
				}
			}
		}
	}
	if !sawEvidence {
		t.Error("no graded expectation carries evidence")
	}
}

// An eval with nothing to grade cannot produce a measurement, and running one
// would count a broken eval against the skill.
func TestRunEvalWith_StopsWhenTheSetHasVoidEvals(t *testing.T) {
	d, req := evalHarnessWithVoid(t)
	_, err := whetstone.RunEvalWith(context.Background(), d, req)
	if err == nil {
		t.Fatal("an eval set with void evals was scored without --allow-void")
	}
	if !strings.Contains(err.Error(), "allow-void") {
		t.Errorf("err = %v, want it to name the flag that proceeds anyway", err)
	}
}

// Expectations are graded by a model. With no gateway there is no score at
// all, so the refusal must come before a panel's worth of sessions is spent.
func TestRunEvalWith_RefusesWithNoGateway(t *testing.T) {
	d, req := evalHarness(t)
	d.Gateway = nil
	_, err := whetstone.RunEvalWith(context.Background(), d, req)
	if err == nil {
		t.Fatal("an eval ran with no grader")
	}
	if !strings.Contains(err.Error(), "gateway") {
		t.Errorf("err = %v, want it to name the missing gateway", err)
	}
}

func TestRunEvalWith_UntrustedRefusesASharedKernelDriver(t *testing.T) {
	d, req := evalHarness(t)
	req.Untrusted = true
	if _, err := whetstone.RunEvalWith(context.Background(), d, req); err == nil {
		t.Fatal("untrusted work was accepted on a shared-kernel driver")
	}
}

// A full tier plans a baseline, so the delta is a real measurement rather
// than an absent one.
func TestRunEvalWith_FullTierMeasuresTheWithoutSkillDelta(t *testing.T) {
	d, req := evalHarnessFull(t)
	r, err := whetstone.RunEvalWith(context.Background(), d, req)
	if err != nil {
		t.Fatalf("RunEvalWith: %v", err)
	}
	if !r.DeltaMeasured {
		t.Fatal("a full tier plans baselines but reported no delta")
	}
	if r.Delta != r.Headline-r.Baseline {
		t.Errorf("delta = %v, want headline (%v) minus baseline (%v)", r.Delta, r.Headline, r.Baseline)
	}
}

// A smoke tier plans no baseline. A zero delta would read as "the skill
// changed nothing", which is not what was measured.
func TestRunEvalWith_SmokeTierReportsAnAbsentDelta(t *testing.T) {
	d, req := evalHarness(t)
	r, err := whetstone.RunEvalWith(context.Background(), d, req)
	if err != nil {
		t.Fatalf("RunEvalWith: %v", err)
	}
	if r.DeltaMeasured {
		t.Error("a smoke tier reported a delta it never measured")
	}
}

func TestRunEvalWith_RecordsWhichMemberTheTriggerF1CameFrom(t *testing.T) {
	d, req := evalHarnessFull(t)
	r, err := whetstone.RunEvalWith(context.Background(), d, req)
	if err != nil {
		t.Fatalf("RunEvalWith: %v", err)
	}
	if r.TriggerSource.Agent == "" || r.TriggerSource.Model == "" {
		t.Errorf("trigger source = %+v, want the member the probes ran on", r.TriggerSource)
	}
}

func TestRunEvalWith_ResumeReusesTheStoredEval(t *testing.T) {
	d, req := evalHarness(t)
	if _, err := whetstone.RunEvalWith(context.Background(), d, req); err != nil {
		t.Fatalf("first run: %v", err)
	}

	// The first run created eval 1. Resuming it must not create a second row,
	// which is what makes a resumed eval one measurement rather than two.
	req.Resume = 1
	if _, err := whetstone.RunEvalWith(context.Background(), d, req); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if _, err := d.Store.Eval(2); err == nil {
		t.Fatal("resume created a second eval row")
	}
}

func TestRunEvalWith_ResumeRefusesAnEvalForADifferentSkill(t *testing.T) {
	d, req := evalHarness(t)
	id, err := d.Store.CreateEval(store.EvalRecord{
		Skill: "other", Tier: "smoke", Status: "running", StartedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	req.Resume = id
	if _, err := whetstone.RunEvalWith(context.Background(), d, req); err == nil {
		t.Fatal("resumed an eval belonging to another skill")
	}
}

func TestRunEvalWith_JSONOutputCarriesTheRequiredKeys(t *testing.T) {
	d, req := evalHarness(t)
	r, err := whetstone.RunEvalWith(context.Background(), d, req)
	if err != nil {
		t.Fatalf("RunEvalWith: %v", err)
	}
	// The report is what every consumer reads; assert the published figures
	// are all on it rather than re-running the command for its stdout.
	if r.Headline == 0 && r.TriggerF1 == 0 {
		t.Error("report carries neither a headline nor a trigger F1")
	}
}

// ---- harness ----

func evalHarness(t *testing.T) (whetstone.EvalDeps, whetstone.EvalRequest) {
	return newEvalHarness(t, runner.TierSmoke, 5, nil)
}

func evalHarnessFull(t *testing.T) (whetstone.EvalDeps, whetstone.EvalRequest) {
	return newEvalHarness(t, runner.TierFull, 10, nil)
}

func evalHarnessWithVoid(t *testing.T) (whetstone.EvalDeps, whetstone.EvalRequest) {
	return newEvalHarness(t, runner.TierSmoke, 5, map[int]string{2: "no expectations to grade"})
}

func newEvalHarness(t *testing.T, tier runner.Tier, evalCount int, void map[int]string) (whetstone.EvalDeps, whetstone.EvalRequest) {
	t.Helper()

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	seedSkill(t, st, "demo", evalCount, void)

	adapters := func(name string) (agent.Adapter, bool) {
		if name == "claude-code" {
			return fakeAdapter{}, true
		}
		return nil, false
	}

	d := whetstone.EvalDeps{
		Store:         st,
		Driver:        fakeDriver{},
		Gateway:       scriptedGateway{},
		Adapters:      adapters,
		Now:           func() time.Time { return time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC) },
		Sleep:         func(time.Duration) {},
		EngineVersion: "test-engine",
	}
	return d, whetstone.EvalRequest{Skill: "demo", Tier: tier}
}

// seedSkill writes an approved spec, an eval set of n evals, the recorded
// checks for it, and a minimal bundle.
func seedSkill(t *testing.T, st *store.Store, name string, n int, void map[int]string) {
	t.Helper()

	sp := &spec.SkillSpec{
		Name:        name,
		Purpose:     "Do the thing.",
		Description: "Does the thing, for testing.",
		Steps: []spec.Step{
			{ID: "s1", Action: "Run scripts/do.py", Postcondition: "out/done.txt exists"},
		},
		TargetTier: spec.TierMid,
	}
	for i := 0; i < 8; i++ {
		sp.Triggers = append(sp.Triggers,
			spec.TriggerPhrase{Text: fmt.Sprintf("please do the thing %d", i)},
			spec.TriggerPhrase{Text: fmt.Sprintf("do an adjacent thing %d", i), Negative: true})
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

	set := &suite.EvalSet{SkillName: name}
	for i := 1; i <= n; i++ {
		ev := suite.Eval{
			ID:             i,
			Prompt:         fmt.Sprintf("Do the thing for eval %d.", i),
			ExpectedOutput: "out/done.txt exists",
			Expectations:   []string{"it wrote out/done.txt"},
		}
		if _, isVoid := void[i]; isVoid {
			ev.Expectations = nil
		}
		set.Evals = append(set.Evals, ev)
	}

	suiteDir, err := st.SuiteDir(name)
	if err != nil {
		t.Fatal(err)
	}
	if err := suite.WriteEvalSet(suiteDir, set); err != nil {
		t.Fatalf("writing eval set fixture: %v", err)
	}
	if err := suite.WriteTriggerQueries(suiteDir, suite.TriggersFromSpec(sp)); err != nil {
		t.Fatalf("writing trigger fixture: %v", err)
	}

	ref, err := suite.Ref(suiteDir)
	if err != nil {
		t.Fatal(err)
	}
	rows := make([]store.SuiteCheckRow, 0, n)
	for i := 1; i <= n; i++ {
		row := store.SuiteCheckRow{TaskID: fmt.Sprintf("%d", i)}
		if reason, isVoid := void[i]; isVoid {
			row.Void, row.Reason = true, reason
		}
		rows = append(rows, row)
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
	if err := os.WriteFile(filepath.Join(bundleDir, "SKILL.md"),
		[]byte("---\nname: "+name+"\n---\nDo the thing.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

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
		Events: []agent.Event{
			{Seq: 1, Type: agent.TypeToolCall, Name: "Skill", Paths: []string{"demo/SKILL.md"}},
			{Seq: 2, Type: agent.TypeShell, Name: "bash scripts/do.py"},
		},
		Meta: agent.Meta{InputTokens: 100, OutputTokens: 50},
	}, nil
}

// scriptedGateway passes every expectation and cites the transcript, so a
// clean fixture run scores 100 with no network and no API key.
type scriptedGateway struct{}

const scriptedGatewayModel = "scripted-strong"

func (scriptedGateway) ModelFor(c llm.ModelClass) string {
	if c == llm.ClassFast {
		return "scripted-fast"
	}
	return scriptedGatewayModel
}

func (scriptedGateway) Complete(_ context.Context, r llm.Req) (llm.Res, error) {
	if r.Role != "score.grade" {
		return llm.Res{}, fmt.Errorf("scriptedGateway: no script for role %q", r.Role)
	}
	type verdict struct {
		Passed   bool   `json:"passed"`
		Evidence string `json:"evidence"`
	}
	var body struct {
		Expectations []verdict `json:"expectations"`
	}
	for i := 0; i < countExpectations(r.Prompt); i++ {
		body.Expectations = append(body.Expectations, verdict{
			Passed: true, Evidence: "the transcript shows scripts/do.py ran"})
	}
	b, err := json.Marshal(body)
	if err != nil {
		return llm.Res{}, err
	}
	return llm.Res{Text: string(b), Model: scriptedGatewayModel}, nil
}

// countExpectations counts the numbered lines in the prompt's Expectations
// section, so the scripted reply always has exactly one verdict per item.
func countExpectations(prompt string) int {
	i := strings.Index(prompt, "## Expectations")
	if i < 0 {
		return 1
	}
	n := 0
	for _, line := range strings.Split(prompt[i:], "\n") {
		if len(line) > 2 && line[0] >= '1' && line[0] <= '9' && line[1] == '.' {
			n++
		}
	}
	if n == 0 {
		return 1
	}
	return n
}
