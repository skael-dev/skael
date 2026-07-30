package whetstone_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
		Gateway:       scriptedGateway{},
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
type scriptedGateway struct{}

func (scriptedGateway) Complete(_ context.Context, r llm.Req) (llm.Res, error) {
	switch r.Role {
	case "score.pairwise":
		q := shortQuote(extractSection(r.Prompt, "Candidate A:\n", "\n\nCandidate B:"))
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
