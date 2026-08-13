package whetstone

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/skael-dev/skael/internal/eval/agent"
	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/eval/llm/agentcli"
	"github.com/skael-dev/skael/internal/eval/provider"
	"github.com/skael-dev/skael/internal/eval/report"
	"github.com/skael-dev/skael/internal/eval/runner"
	"github.com/skael-dev/skael/internal/eval/sandbox"
	"github.com/skael-dev/skael/internal/eval/sandbox/docker"
	"github.com/skael-dev/skael/internal/eval/sandbox/imagespec"
	"github.com/skael-dev/skael/internal/eval/score"
	"github.com/skael-dev/skael/internal/eval/store"
	"github.com/skael-dev/skael/internal/eval/suite"
	"github.com/skael-dev/skael/internal/ui"
)

// EvalDeps is everything RunEvalWith needs, injected so a test can supply a
// fake driver, adapter registry, and gateway with no Docker, no network, and
// no LLM subscription. RunEval (below) resolves these from the environment
// the same way `whetstone doctor` and `whetstone suite check` do.
type EvalDeps struct {
	Store  *store.Store
	Driver sandbox.Driver
	// Gateway is nil when no LLM backend is available. A nil Gateway degrades
	// Uplift to the pass-rate fallback; it does not fail the eval — the
	// deterministic pillars (Reliability, TriggerF1, Efficiency) are still a
	// usable measurement without a judge.
	Gateway  llm.Gateway
	Adapters func(name string) (agent.Adapter, bool)
	// Now defaults to time.Now.
	Now func() time.Time
	// Sleep defaults to time.Sleep; the runner's rate-limit backoff uses it.
	Sleep         func(time.Duration)
	EngineVersion string
	// WorkspaceRoot is passed through to runner.Options.WorkspaceRoot — see
	// there for why a containerized runner has to set it. Empty is correct
	// for the interactive CLI, which always shares a filesystem with the
	// daemon it starts sandboxes on.
	WorkspaceRoot string
	// PanelModels overrides the shipped panel's model ids, most capable
	// first. Already-resolved values rather than env lookups, so RunEvalWith
	// carries no policy — see provider.Config.PanelModels.
	PanelModels []string
	// PanelBaseURL is carried for diagnostics only, so an all-unhealthy panel
	// can name the endpoint that rejected its models.
	PanelBaseURL string
}

// EvalRequest is one `whetstone eval` invocation.
type EvalRequest struct {
	Skill       string
	Tier        runner.Tier
	Agents      []string
	Models      []string
	Concurrency int
	Untrusted   bool
	AllowVoid   bool
	// Resume, when non-zero, reuses that eval id rather than starting a new
	// one — see RunEvalWith for the suite/panel mismatch guard.
	Resume int64
	// TaskFilter, when non-empty, restricts scoring to tasks whose ID
	// appears in it — the repair loop's way of re-running only the affected
	// dev tasks (or, at the end, only the holdout split) without touching
	// the suite on disk. Filtering happens after the suite is loaded, so
	// SuiteRef still identifies the full suite the tasks were drawn from.
	TaskFilter []string
}

// baseEnsurer is satisfied by *docker.Driver but not by every sandbox.Driver
// (a test fake, in particular). RunEvalWith calls it opportunistically: a
// driver with no base-image concept has nothing to ensure.
type baseEnsurer interface {
	EnsureBase(ctx context.Context, slim bool) error
}

// RunEvalWith runs one evaluation end to end: load the approved spec and eval
// set, refuse an eval against unchecked or void evals, plan and execute the
// panel, grade every session, and persist and write the report.
func RunEvalWith(ctx context.Context, d EvalDeps, req EvalRequest) (*report.Report, error) {
	if d.Store == nil || d.Driver == nil || d.Adapters == nil {
		return nil, errors.New("whetstone eval: needs a store, a driver, and an adapter registry")
	}
	// The score is an expectation pass rate, and expectations are graded by a
	// model. Without a gateway there is no score at all, so this fails here
	// rather than after spending a panel's worth of sessions.
	if d.Gateway == nil {
		return nil, errors.New("whetstone eval: needs an LLM gateway to grade expectations; run `whetstone doctor` to check your setup")
	}
	now := d.Now
	if now == nil {
		now = time.Now
	}
	sleepFn := d.Sleep
	if sleepFn == nil {
		sleepFn = time.Sleep
	}

	// 1. Approved spec, the eval set, and its content ref.
	sp, specVersion, err := d.Store.LoadSpec(req.Skill)
	if err != nil {
		return nil, err
	}
	if !isApproved(d.Store, req.Skill, specVersion) {
		return nil, fmt.Errorf("%s spec version %d is not approved; review it with `whetstone spec show %s` and approve it with `whetstone spec approve %s`",
			req.Skill, specVersion, req.Skill, req.Skill)
	}

	suiteDir, err := d.Store.SuiteDir(req.Skill)
	if err != nil {
		return nil, err
	}
	set, err := suite.LoadEvalSet(suiteDir)
	if err != nil {
		return nil, fmt.Errorf("whetstone eval: loading the eval set for %s: %w (run `whetstone suite gen %s` first)", req.Skill, err, req.Skill)
	}
	triggers, err := suite.LoadTriggerQueries(suiteDir)
	if err != nil {
		return nil, err
	}
	suiteRef, err := suite.Ref(suiteDir)
	if err != nil {
		return nil, err
	}

	// 2. The eval set must have been checked, and cleanly.
	checks, err := d.Store.SuiteChecks(req.Skill, suiteRef)
	if err != nil {
		return nil, err
	}
	if len(checks) == 0 {
		return nil, fmt.Errorf("whetstone eval: no check recorded for %s at eval set %s; run `whetstone suite check %s` first",
			req.Skill, suite.ShortRef(suiteRef), req.Skill)
	}
	voidSet := map[int]bool{}
	var voidTasks []report.VoidTask
	for _, c := range checks {
		if !c.Void {
			continue
		}
		id, cerr := strconv.Atoi(c.TaskID)
		if cerr != nil {
			return nil, fmt.Errorf("whetstone eval: recorded check names eval %q, which is not an eval id: %w", c.TaskID, cerr)
		}
		voidSet[id] = true
		voidTasks = append(voidTasks, report.VoidTask{TaskID: c.TaskID, Reason: c.Reason})
	}
	if len(voidSet) > 0 && !req.AllowVoid {
		return nil, fmt.Errorf("whetstone eval: %d of %d evals are void for %s; fix them and re-run `whetstone suite check %s`, or pass --allow-void to exclude them",
			len(voidSet), len(checks), req.Skill, req.Skill)
	}

	// 3. The model panel. A caller that named one always wins; this only fills
	// the default. The shipped default names bare Claude Code aliases, which
	// mean nothing to a gateway that namespaces its identifiers — so when the
	// boundary resolved model ids for a custom gateway, build the default out
	// of those instead.
	agents, models := req.Agents, req.Models
	if len(agents) == 0 && len(models) == 0 && len(d.PanelModels) > 0 {
		shipped := runner.PanelFor(req.Tier)
		agents = []string{shipped[0].Agent}
		// One configured model per shipped member, in order: the extra models
		// only mean anything at the deep tier, which is the only tier with a
		// floor member to give them to.
		models = d.PanelModels
		if len(models) > len(shipped) {
			models = models[:len(shipped)]
		}
	}
	panel := runner.PanelFor(req.Tier)
	if len(agents) > 0 || len(models) > 0 {
		panel, err = runner.ParsePanel(agents, models)
		if err != nil {
			return nil, err
		}
	}

	// 4. The trust gate, before anything is prepared.
	if err := sandbox.CheckPolicy(d.Driver, req.Untrusted); err != nil {
		return nil, err
	}

	// 5. The image the panel runs against.
	baseTag := os.Getenv("WHETSTONE_BASE_TAG")
	if be, ok := d.Driver.(baseEnsurer); ok {
		if err := be.EnsureBase(ctx, baseTag == imagespec.SlimBaseTag); err != nil {
			return nil, fmt.Errorf("whetstone eval: preparing base image: %w", err)
		}
	}
	image, err := d.Driver.Prepare(ctx, sandbox.EnvSpec{Skill: sp.Name, Deps: sp.Deps, BaseTag: baseTag})
	if err != nil {
		return nil, fmt.Errorf("whetstone eval: preparing image: %w", err)
	}
	if _, err := d.Driver.Snapshot(ctx, image); err != nil {
		return nil, fmt.Errorf("whetstone eval: snapshotting image: %w", err)
	}

	rn, err := runner.New(runner.Options{
		Store: d.Store, Driver: d.Driver, Adapters: d.Adapters,
		Concurrency: req.Concurrency, Untrusted: req.Untrusted,
		Sleep: sleepFn, Logger: ui.Info,
		WorkspaceRoot: d.WorkspaceRoot,
	})
	if err != nil {
		return nil, err
	}

	// 6. Health-probe the panel before spending any session on it.
	health, err := rn.ProbePanel(ctx, panel, image)
	if err != nil {
		return nil, err
	}
	healthy := map[runner.Member]bool{}
	healthDetail := map[runner.Member]string{}
	for _, h := range health {
		healthy[h.Member] = h.OK
		healthDetail[h.Member] = h.Detail
	}
	if err := checkPanelHealth(health, d.PanelBaseURL); err != nil {
		return nil, err
	}

	// 7. Reuse a resumed eval row, or start a new one.
	panelJSON, err := json.Marshal(panel)
	if err != nil {
		return nil, err
	}
	var evalID int64
	// startedAt is the eval's own start time, not the moment this process
	// happened to resume it.
	startedAt := now()
	if req.Resume != 0 {
		existing, err := d.Store.Eval(req.Resume)
		if err != nil {
			return nil, err
		}
		if existing.Skill != req.Skill {
			return nil, fmt.Errorf("whetstone eval: --resume %d is an eval for %q, not %q", req.Resume, existing.Skill, req.Skill)
		}
		if existing.SuiteRef != suiteRef {
			return nil, fmt.Errorf("whetstone eval: --resume %d was measured against eval set %s, the current one is %s; resuming across a change would silently mix two measurements",
				req.Resume, suite.ShortRef(existing.SuiteRef), suite.ShortRef(suiteRef))
		}
		if string(existing.ModelPanel) != string(panelJSON) {
			return nil, fmt.Errorf("whetstone eval: --resume %d was measured against a different model panel; resuming would silently mix two measurements", req.Resume)
		}
		evalID = existing.ID
		startedAt = existing.StartedAt
	} else {
		evalID, err = d.Store.CreateEval(store.EvalRecord{
			Skill: req.Skill, SpecVersion: specVersion, Tier: string(req.Tier), SuiteRef: suiteRef,
			EngineVersion: d.EngineVersion, ModelPanel: panelJSON, Seed: 1,
			StartedAt: startedAt, Status: "running",
		})
		if err != nil {
			return nil, err
		}
	}

	// 8. Plan and execute.
	plan, err := runner.BuildPlan(req.Tier, panel, set, voidSet, triggers)
	if err != nil {
		return nil, err
	}
	bundleDir, err := d.Store.SkillDir(req.Skill)
	if err != nil {
		return nil, err
	}
	distractors, err := suite.Distractors()
	if err != nil {
		return nil, err
	}
	execRes, err := rn.Execute(ctx, evalID, runner.ExecuteInput{
		Skill: req.Skill, BundleDir: bundleDir, SuiteDir: suiteDir, Image: image,
		Plan: plan, Healthy: healthy, Distractors: distractors,
	})
	if err != nil {
		return nil, err
	}

	// 9. Grade every session that produced a transcript.
	grader, err := score.NewGrader(d.Gateway)
	if err != nil {
		return nil, err
	}
	graded, err := gradeOutcomes(ctx, grader, plan, execRes.Outcomes, req.Concurrency)
	if err != nil {
		return nil, err
	}
	for key, g := range graded {
		doc, merr := json.Marshal(g.Expectations)
		if merr != nil {
			return nil, merr
		}
		if err := d.Store.SaveGrade(evalID, store.RunGrade{
			Key: key, Passed: g.Passed, Total: g.Total, Doc: doc,
		}); err != nil {
			return nil, err
		}
	}

	// 10. Score each member from its grades.
	cliVersion := agentCLIVersion()
	modelPanelOut := make([]report.PanelMember, 0, len(panel))
	for _, m := range panel {
		modelPanelOut = append(modelPanelOut, report.PanelMember{
			Agent: m.Agent, Model: m.Model, Class: string(m.Class), CLIVersion: cliVersion})
	}

	scheduled := map[runner.Member]bool{}
	for _, k := range plan.Runs {
		for _, m := range panel {
			if m.Agent == k.Agent && m.Model == k.Model {
				scheduled[m] = true
			}
		}
	}

	tasks := newTaskAgg()
	var members []report.MemberInput
	for _, m := range panel {
		if !scheduled[m] {
			continue
		}
		mi := report.MemberInput{
			Member:  report.PanelMember{Agent: m.Agent, Model: m.Model, Class: string(m.Class), CLIVersion: cliVersion},
			Healthy: healthy[m],
			Detail:  healthDetail[m],
		}
		if !mi.Healthy {
			members = append(members, mi)
			continue
		}
		s, partial, reason, err := memberScore(plan, execRes.Outcomes, graded, m, runner.CondSkill, tasks)
		if err != nil {
			return nil, fmt.Errorf("whetstone eval: member %s/%s: %w", m.Agent, m.Model, err)
		}
		mi.Score, mi.MetaPartial, mi.MetaPartialReason = s, partial, reason
		members = append(members, mi)
	}

	// The baseline runs on the primary member only, so the delta is that
	// member's score against its own without-skill runs. Comparing it to the
	// panel minimum would subtract two different members' numbers.
	var baseline float64
	var baselineMeasured, baselineWipeout bool
	if runner.BaselinePlanned(*plan) {
		primary := panel[0]
		b, _, _, berr := memberScore(plan, execRes.Outcomes, graded, primary, runner.CondBaseline, tasks)
		if berr == nil {
			baseline, baselineMeasured = b, true
			baselineWipeout = b == 0
		} else {
			ui.Warn("whetstone eval: no baseline score — %v", berr)
		}
	}

	tokensSkill := medianTokens(execRes.Outcomes, runner.CondSkill)
	tokensBaseline := medianTokens(execRes.Outcomes, runner.CondBaseline)

	triggerF1, triggerInferred, triggerUnknown, triggerSource, err := scoreTriggers(req.Skill, execRes.Probes)
	if err != nil {
		return nil, err
	}

	rep, err := report.Compose(report.ComposeInput{
		Skill: req.Skill, SpecVersion: specVersion, Tier: string(req.Tier), SuiteRef: suiteRef,
		EngineVersion: d.EngineVersion, ModelPanel: modelPanelOut, PanelComplete: execRes.PanelComplete,
		Members: members, Tasks: tasks.slice(), Void: voidTasks,
		Baseline: baseline, BaselineMeasured: baselineMeasured, BaselineWipeout: baselineWipeout,
		TokensMedian: tokensSkill, TokensMedianBaseline: tokensBaseline,
		TriggerF1: triggerF1, TriggerInferred: triggerInferred,
		TriggerUnknown: triggerUnknown, TriggerSource: triggerSource,
		GraderModel: graderModel(d.Gateway, graded),
		StartedAt:   startedAt, FinishedAt: now(),
	})
	if err != nil {
		return nil, err
	}

	// 11. Persist.
	for _, mr := range rep.Members {
		if err := d.Store.SaveScore(evalID, store.ScoreRow{
			Agent: mr.Member.Agent, Model: mr.Member.Model,
			Effectiveness: mr.Effectiveness, Healthy: mr.Healthy,
		}); err != nil {
			return nil, err
		}
	}

	var repBuf bytes.Buffer
	if err := rep.Save(&repBuf); err != nil {
		return nil, err
	}
	if err := d.Store.SaveReport(evalID, repBuf.Bytes(), store.ReportMeta{
		Headline: rep.Headline, PanelComplete: rep.PanelComplete,
	}); err != nil {
		return nil, err
	}
	if err := d.Store.FinishEval(evalID, "done"); err != nil {
		return nil, err
	}

	// 12. Write the sidecar report files and announce the result.
	evalDir, err := d.Store.EvalDir(req.Skill)
	if err != nil {
		return nil, err
	}
	reportsDir := filepath.Join(evalDir, "reports", strconv.FormatInt(evalID, 10))
	if err := os.MkdirAll(reportsDir, 0o755); err != nil {
		return nil, fmt.Errorf("whetstone eval: creating report directory: %w", err)
	}
	if err := writeReportFile(filepath.Join(reportsDir, "report.json"), rep.Save); err != nil {
		return nil, err
	}
	if err := writeReportFile(filepath.Join(reportsDir, "report.html"), rep.HTML); err != nil {
		return nil, err
	}

	if ui.JSONMode {
		if err := ui.PrintJSON(map[string]any{
			"eval_id": evalID, "skill": req.Skill, "tier": string(req.Tier), "suite_ref": suiteRef,
			"headline": rep.Headline, "baseline": rep.Baseline, "delta": rep.Delta,
			"delta_measured": rep.DeltaMeasured, "trigger_f1": rep.TriggerF1,
			"panel_complete": rep.PanelComplete,
		}); err != nil {
			return nil, err
		}
	} else {
		// Every other whetstone human-facing output goes to stderr so --json
		// stdout stays clean and parseable; this must match.
		fmt.Fprint(os.Stderr, RenderEvalSummary(rep, evalID, req.Skill))
	}

	return rep, nil
}

// gradeOutcomes grades every outcome that produced a transcript, keyed by run.
//
// Grading is one gateway call per session and the calls are independent, so
// they run concurrently: done in series, grading would cost more wall clock
// than the sessions it grades.
func gradeOutcomes(ctx context.Context, g *score.Grader, plan *runner.Plan, outs []runner.Outcome, concurrency int) (map[store.RunKey]score.Grade, error) {
	if concurrency <= 0 {
		concurrency = 4
	}
	var (
		mu     sync.Mutex
		wg     sync.WaitGroup
		sem    = make(chan struct{}, concurrency)
		out    = map[store.RunKey]score.Grade{}
		errs   []error
		gradeF = func(o runner.Outcome) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			ev, ok := plan.EvalByID(o.Key.TaskID)
			if !ok || len(ev.Expectations) == 0 {
				return
			}
			grade, err := g.Grade(ctx, ev.Expectations, score.Run{
				Prompt:         ev.Prompt,
				ExpectedOutput: ev.ExpectedOutput,
				Transcript:     loadTranscript(o.ArtifactDir),
				Outputs:        renderOutputs(o.ArtifactDir),
			})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, fmt.Errorf("grading eval %s (%s attempt %d): %w",
					o.Key.TaskID, o.Key.Condition, o.Key.Attempt, err))
				return
			}
			out[o.Key] = grade
		}
	)

	for _, o := range outs {
		// A session that errored or timed out produced no measurement. It is
		// dropped rather than graded as a failure: an infrastructure fault is
		// not evidence about the skill.
		if o.Status != store.StatusOK {
			continue
		}
		wg.Add(1)
		go gradeF(o)
	}
	wg.Wait()

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return out, nil
}

// graderModel names the judge for a report.
//
// A gateway that knows its model in advance is the authority, because it names
// what every call asked for. A subscription CLI knows nothing in advance, so
// its answers are the only record of what judged.
//
// The declared name wins deliberately. An API gateway resolves an alias such
// as "sonnet" to a dated id, and a switch to that id makes every new score
// non-comparable with every existing one. That change stands on its own merits
// and is not this one.
func graderModel(g llm.Gateway, graded map[store.RunKey]score.Grade) string {
	if declared := g.ModelFor(llm.ClassStrong); declared != "" {
		return declared
	}
	return observedGraderModel(graded)
}

// observedGraderModel names the model or models that graded a run, as the
// gateway reported them in its answers.
//
// Two distinct models join rather than one winning. One score graded by two
// models is one score with two judges, and Report.Comparable then refuses to
// chart it beside a single-judge score. That refusal is the honest outcome, and
// a silent first-wins hides it.
func observedGraderModel(graded map[store.RunKey]score.Grade) string {
	seen := map[string]bool{}
	for _, g := range graded {
		if g.Model != "" {
			seen[g.Model] = true
		}
	}
	names := make([]string, 0, len(seen))
	for m := range seen {
		names = append(names, m)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// memberScore is one member's 0–100 score under one condition, and the
// per-eval tallies that back it.
func memberScore(plan *runner.Plan, outs []runner.Outcome, graded map[store.RunKey]score.Grade,
	m runner.Member, cond store.Condition, tasks *taskAgg) (float64, bool, string, error) {

	byEval := map[string][]score.Grade{}
	var metaPartial bool
	var metaReason string
	for _, o := range outs {
		if o.Key.Agent != m.Agent || o.Key.Model != m.Model || o.Key.Condition != cond {
			continue
		}
		if o.MetaPartial && !metaPartial {
			metaPartial, metaReason = true, o.MetaPartialReason
		}
		g, ok := graded[o.Key]
		if !ok {
			continue
		}
		byEval[o.Key.TaskID] = append(byEval[o.Key.TaskID], g)
		tasks.addGrade(o.Key.TaskID, report.GradeNote{
			Model: m.Model, Condition: cond, Attempt: o.Key.Attempt, Expectations: g.Expectations,
		})
	}

	ids := make([]string, 0, len(byEval))
	for id := range byEval {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	rates := make([]float64, 0, len(ids))
	for _, id := range ids {
		gs := byEval[id]
		rate, err := score.EvalRate(gs)
		if err != nil {
			return 0, metaPartial, metaReason, fmt.Errorf("eval %s: %w", id, err)
		}
		rates = append(rates, rate)

		passed, total := 0, 0
		for _, g := range gs {
			passed += g.Passed
			total += g.Total
		}
		tasks.addCondition(id, report.ConditionReport{
			Condition: cond, Model: m.Model, Passes: passed, Runs: total,
		})
	}

	s, err := score.MemberScore(rates)
	if err != nil {
		return 0, metaPartial, metaReason, err
	}
	return s, metaPartial, metaReason, nil
}

// scoreTriggers turns the trigger smoke check's probes into an F1.
//
// A tier that plans no probes measures nothing here and reports 1.0: an
// unmeasured check must not read as a failed one. The release precondition
// that reads this figure is applied where a version is released, not here.
func scoreTriggers(skill string, probes []runner.ProbeOutcome) (f1 float64, inferred bool, unknown int, source report.PanelMember, err error) {
	if len(probes) == 0 {
		return 1, false, 0, report.PanelMember{}, nil
	}
	pm := probes[0].Probe.Member
	source = report.PanelMember{Agent: pm.Agent, Model: pm.Model, Class: string(pm.Class)}

	ps := make([]score.Probe, 0, len(probes))
	for _, p := range probes {
		if p.Unknown {
			ps = append(ps, score.Probe{Prompt: p.Probe.Prompt, Positive: p.Probe.Positive, Unknown: true, Reason: p.Reason})
			continue
		}
		fired, inf := score.DetectFiring(skill, p.Caps, p.Events)
		ps = append(ps, score.Probe{Prompt: p.Probe.Prompt, Positive: p.Probe.Positive, Fired: fired, Inferred: inf})
	}
	res, err := score.TriggerF1(ps)
	if err != nil {
		return 0, false, 0, source, err
	}
	return res.F1, res.AnyInferred, res.Unknown, source, nil
}

// medianTokens is the median total token spend across the sessions run under
// one condition. Zero when nothing was measured, which the report omits.
func medianTokens(outs []runner.Outcome, cond store.Condition) int64 {
	var totals []int64
	for _, o := range outs {
		if o.Key.Condition != cond || o.Status != store.StatusOK {
			continue
		}
		totals = append(totals, o.Meta.InputTokens+o.Meta.OutputTokens)
	}
	m, err := score.MedianTokens(totals)
	if err != nil {
		return 0
	}
	return m
}

// taskAgg accumulates each eval's per-condition tallies and graded runs across
// every scored member, so the report shows the measurements the member scores
// were computed from.
type taskAgg struct{ byID map[string]*report.TaskInput }

func newTaskAgg() *taskAgg { return &taskAgg{byID: map[string]*report.TaskInput{}} }

func (t *taskAgg) get(id string) *report.TaskInput {
	ti, ok := t.byID[id]
	if !ok {
		ti = &report.TaskInput{TaskID: id}
		t.byID[id] = ti
	}
	return ti
}

func (t *taskAgg) addCondition(id string, c report.ConditionReport) {
	ti := t.get(id)
	ti.Conditions = append(ti.Conditions, c)
}

func (t *taskAgg) addGrade(id string, g report.GradeNote) {
	ti := t.get(id)
	ti.Grades = append(ti.Grades, g)
}

func (t *taskAgg) slice() []report.TaskInput {
	ids := make([]string, 0, len(t.byID))
	for id := range t.byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	out := make([]report.TaskInput, 0, len(ids))
	for _, id := range ids {
		ti := t.byID[id]
		sort.Slice(ti.Conditions, func(i, j int) bool {
			if ti.Conditions[i].Condition != ti.Conditions[j].Condition {
				return ti.Conditions[i].Condition < ti.Conditions[j].Condition
			}
			return ti.Conditions[i].Model < ti.Conditions[j].Model
		})
		sort.Slice(ti.Grades, func(i, j int) bool {
			if ti.Grades[i].Condition != ti.Grades[j].Condition {
				return ti.Grades[i].Condition < ti.Grades[j].Condition
			}
			if ti.Grades[i].Model != ti.Grades[j].Model {
				return ti.Grades[i].Model < ti.Grades[j].Model
			}
			return ti.Grades[i].Attempt < ti.Grades[j].Attempt
		})
		out = append(out, *ti)
	}
	return out
}

// agentCLIVersion is the version of the agent CLI on this host's PATH,
// recorded on every panel member so Report.Comparable can tell a CLI upgrade
// between two runs from a change in the skill.
func agentCLIVersion() string {
	bin, err := agentcli.Detect()
	if err != nil {
		return ""
	}
	return probeVersion(bin)
}

// loadTranscript reads a run's recorded transcript, or "" if it cannot be
// read. The grader is told to fail an expectation it cannot verify, so a
// missing transcript grades hard rather than aborting the eval.
func loadTranscript(artifactDir string) string {
	if artifactDir == "" {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(artifactDir, "transcript.raw"))
	if err != nil {
		return ""
	}
	return string(b)
}

// maxOutputBytes bounds how much of one output file reaches the grader. A
// skill that writes a large artifact would otherwise push the transcript out
// of the request.
const maxOutputBytes = 32 << 10

// renderOutputs renders the files a session produced, so the grader can check
// an expectation against what was written rather than against the agent's own
// account of it.
func renderOutputs(artifactDir string) string {
	if artifactDir == "" {
		return ""
	}
	root := filepath.Join(artifactDir, "outputs")
	var b strings.Builder
	err := filepath.WalkDir(root, func(p string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return nil
		}
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return nil
		}
		fmt.Fprintf(&b, "### %s\n", filepath.ToSlash(rel))
		if !utf8.Valid(data) {
			fmt.Fprintf(&b, "(binary, %d bytes)\n\n", len(data))
			return nil
		}
		if len(data) > maxOutputBytes {
			b.Write(data[:maxOutputBytes])
			fmt.Fprintf(&b, "\n… truncated at %d bytes\n\n", maxOutputBytes)
			return nil
		}
		b.Write(data)
		b.WriteString("\n\n")
		return nil
	})
	if err != nil {
		return ""
	}
	return b.String()
}

// writeReportFile creates path and hands it to write, closing it either way.
func writeReportFile(path string, write func(w io.Writer) error) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("whetstone eval: creating %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	if err := write(f); err != nil {
		return fmt.Errorf("whetstone eval: writing %s: %w", path, err)
	}
	return nil
}

// RunEval resolves EvalDeps from the environment — the workspace store, a
// docker sandbox driver, the LLM gateway `whetstone doctor` would choose, and
// the linked-in adapter registry — and runs the eval.
func RunEval(ctx context.Context, req EvalRequest) error {
	st, err := openStore()
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	baseTag := os.Getenv("WHETSTONE_BASE_TAG")
	drv, err := docker.New(docker.Options{BaseTag: baseTag, Logger: ui.Info})
	if err != nil {
		return fmt.Errorf("whetstone eval: %w; run `whetstone doctor` to check your setup", err)
	}
	// See suitecheck.go's identical call: a prior run killed by something
	// stronger than its own context can leave containers and networks behind.
	drv.Sweep(ctx)

	var gw llm.Gateway
	if g, gerr := newGateway(st.Cache()); gerr == nil {
		gw = g
	}

	p := provider.FromEnv()
	for _, w := range p.Warnings() {
		ui.Warn("%s", w)
	}

	d := EvalDeps{
		Store: st, Driver: drv, Gateway: gw, Adapters: agent.Get,
		Now: time.Now, Sleep: time.Sleep, EngineVersion: buildVersion,
		PanelModels: p.PanelModels(), PanelBaseURL: p.BaseURL,
	}
	_, err = RunEvalWith(ctx, d, req)
	return err
}

var (
	evalTier        string
	evalAgents      []string
	evalModels      []string
	evalConcurrency int
	evalUntrusted   bool
	evalAllowVoid   bool
	evalResume      int64
)

var evalCmd = &cobra.Command{
	Use:   "eval <skill>",
	Short: "Run the model panel against a skill, score it, and write the report",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return RunEval(cmd.Context(), EvalRequest{
			Skill: args[0], Tier: runner.Tier(evalTier), Agents: evalAgents, Models: evalModels,
			Concurrency: evalConcurrency, Untrusted: evalUntrusted, AllowVoid: evalAllowVoid, Resume: evalResume,
		})
	},
}

func init() {
	evalCmd.Flags().StringVar(&evalTier, "tier", string(runner.TierFull), "Tier to run: smoke, full, or deep")
	evalCmd.Flags().StringSliceVar(&evalAgents, "agents", nil, "Panel agents (pass with --models); defaults to the shipped panel")
	evalCmd.Flags().StringSliceVar(&evalModels, "models", nil, "Panel models (pass with --agents); defaults to the shipped panel")
	evalCmd.Flags().IntVar(&evalConcurrency, "concurrency", 0, "Maximum concurrent sessions (0 uses the runner's default)")
	evalCmd.Flags().BoolVar(&evalUntrusted, "untrusted", false, "Treat the skill as untrusted; refused unless the driver is hardware-isolated")
	evalCmd.Flags().BoolVar(&evalAllowVoid, "allow-void", false, "Proceed with void tasks excluded from scoring rather than refusing")
	evalCmd.Flags().Int64Var(&evalResume, "resume", 0, "Resume an existing eval id instead of starting a new one")
	rootCmd.AddCommand(evalCmd)
}
