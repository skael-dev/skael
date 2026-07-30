package whetstone

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/skael-dev/skael/internal/eval/agent"
	"github.com/skael-dev/skael/internal/eval/contract"
	"github.com/skael-dev/skael/internal/eval/drift"
	"github.com/skael-dev/skael/internal/eval/llm"
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
}

// baseEnsurer is satisfied by *docker.Driver but not by every sandbox.Driver
// (a test fake, in particular). RunEvalWith calls it opportunistically: a
// driver with no base-image concept has nothing to ensure.
type baseEnsurer interface {
	EnsureBase(ctx context.Context, slim bool) error
}

// RunEvalWith runs one evaluation end to end: load the approved spec and
// suite, refuse an eval against unchecked or void tasks, plan and execute the
// panel, score the result, and persist and write the report. See the package
// doc for the phase-by-phase breakdown; this function follows it in order.
func RunEvalWith(ctx context.Context, d EvalDeps, req EvalRequest) (*report.Report, error) {
	if d.Store == nil || d.Driver == nil || d.Adapters == nil {
		return nil, errors.New("whetstone eval: needs a store, a driver, and an adapter registry")
	}
	now := d.Now
	if now == nil {
		now = time.Now
	}
	sleepFn := d.Sleep
	if sleepFn == nil {
		sleepFn = time.Sleep
	}

	// 1. Approved spec, written suite, and the suite's content ref.
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
	s, err := suite.Load(suiteDir)
	if err != nil {
		return nil, fmt.Errorf("whetstone eval: loading suite for %s: %w (run `whetstone new %s` first)", req.Skill, err, req.Skill)
	}
	suiteRef, err := suite.Ref(suiteDir)
	if err != nil {
		return nil, err
	}

	// 2. The suite must have been gated, and cleanly.
	checks, err := d.Store.SuiteChecks(req.Skill, suiteRef)
	if err != nil {
		return nil, err
	}
	if len(checks) == 0 {
		return nil, fmt.Errorf("whetstone eval: no suite check recorded for %s at suite %s; an eval against unchecked tasks cannot tell a broken task from a broken skill — run `whetstone suite check %s` first",
			req.Skill, suite.ShortRef(suiteRef), req.Skill)
	}
	voidSet := map[string]bool{}
	var voidTasks []report.VoidTask
	var voidCount int
	for _, c := range checks {
		if c.Void {
			voidSet[c.TaskID] = true
			voidTasks = append(voidTasks, report.VoidTask{TaskID: c.TaskID, Reason: c.Reason})
			voidCount++
		}
	}
	if voidCount > 0 && !req.AllowVoid {
		return nil, fmt.Errorf("whetstone eval: %d of %d tasks are void for %s; fix them and re-run `whetstone suite check %s`, or pass --allow-void to exclude them",
			voidCount, len(checks), req.Skill, req.Skill)
	}

	// 3. The model panel.
	panel, err := runner.ParsePanel(req.Agents, req.Models)
	if err != nil {
		return nil, err
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
	// Snapshot is a no-op on the docker driver; it is still called here so a
	// driver with real checkpoint support (Sprites) needs no change at this
	// call site.
	if _, err := d.Driver.Snapshot(ctx, image); err != nil {
		return nil, fmt.Errorf("whetstone eval: snapshotting image: %w", err)
	}

	ro := runner.Options{
		Store: d.Store, Driver: d.Driver, Adapters: d.Adapters,
		Concurrency: req.Concurrency, Untrusted: req.Untrusted,
		Sleep: sleepFn, Logger: ui.Info,
	}
	rn, err := runner.New(ro)
	if err != nil {
		return nil, err
	}

	// 6. Health-probe the panel before spending any task session on it.
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

	// 7. Reuse a resumed eval row, or start a new one.
	panelJSON, err := json.Marshal(panel)
	if err != nil {
		return nil, err
	}
	var evalID int64
	if req.Resume != 0 {
		existing, err := d.Store.Eval(req.Resume)
		if err != nil {
			return nil, err
		}
		if existing.Skill != req.Skill {
			return nil, fmt.Errorf("whetstone eval: --resume %d is an eval for %q, not %q", req.Resume, existing.Skill, req.Skill)
		}
		if existing.SuiteRef != suiteRef {
			return nil, fmt.Errorf("whetstone eval: --resume %d was measured against suite %s, the current suite is %s; resuming across a changed suite would silently mix two measurements",
				req.Resume, suite.ShortRef(existing.SuiteRef), suite.ShortRef(suiteRef))
		}
		if string(existing.ModelPanel) != string(panelJSON) {
			return nil, fmt.Errorf("whetstone eval: --resume %d was measured against a different model panel; resuming would silently mix two measurements", req.Resume)
		}
		evalID = existing.ID
	} else {
		evalID, err = d.Store.CreateEval(store.EvalRecord{
			Skill: req.Skill, SpecVersion: specVersion, Tier: string(req.Tier), SuiteRef: suiteRef,
			EngineVersion: d.EngineVersion, ModelPanel: panelJSON, Seed: 1,
			StartedAt: now(), Status: "running",
		})
		if err != nil {
			return nil, err
		}
	}

	// 8. Plan and execute.
	plan, err := runner.BuildPlan(req.Tier, panel, s, voidSet)
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

	// 9. Score.
	contractPath, err := d.Store.ContractPath(req.Skill)
	if err != nil {
		return nil, err
	}
	cf, err := os.Open(contractPath)
	if err != nil {
		return nil, fmt.Errorf("whetstone eval: opening contract for %s: %w (run `whetstone new %s` first)", req.Skill, err, req.Skill)
	}
	ct, err := contract.Load(cf)
	_ = cf.Close()
	if err != nil {
		return nil, err
	}

	memberFor := func(agentName, model string) (runner.Member, bool) {
		for _, m := range panel {
			if m.Agent == agentName && m.Model == model {
				return m, true
			}
		}
		return runner.Member{}, false
	}

	grouped := map[runner.Member]*memberOutcomes{}
	scheduled := map[runner.Member]bool{}
	for _, k := range plan.Runs {
		m, ok := memberFor(k.Agent, k.Model)
		if !ok {
			continue
		}
		scheduled[m] = true
	}
	for _, o := range execRes.Outcomes {
		m, ok := memberFor(o.Key.Agent, o.Key.Model)
		if !ok {
			continue
		}
		g := grouped[m]
		if g == nil {
			g = newMemberOutcomes()
			grouped[m] = g
		}
		switch o.Key.Condition {
		case runner.CondSkill:
			g.skill[o.Key.TaskID] = append(g.skill[o.Key.TaskID], o)
		case runner.CondBaseline:
			g.baseline[o.Key.TaskID] = append(g.baseline[o.Key.TaskID], o)
		}
	}

	// Judge calibration runs once, independent of any particular member: it
	// answers "is this judge trustworthy at all", against the fixed labelled
	// set, not against this eval's own sessions.
	var (
		judge          *score.Judge
		judgeTrusted   bool
		judgeKappa     *float64
		judgeLabeledBy string
	)
	if d.Gateway != nil {
		if calSet, cerr := score.Calibration(); cerr == nil {
			if j, jerr := score.NewJudge(score.JudgeOptions{Gateway: d.Gateway, Spec: sp}); jerr == nil {
				if result, rerr := score.Calibrate(ctx, j, calSet); rerr == nil {
					k := result.Kappa
					judgeKappa = &k
					judgeLabeledBy = result.LabeledBy
					judgeTrusted = result.JudgeTrusted()
					judge = j
				}
			}
		}
	}

	// Trigger firing is a property of the skill's description, not of which
	// panel member happened to run the probe (probes run on the primary
	// member only) — so one F1 result is shared across every scored member.
	// A tier that plans no probes (Smoke) measures nothing here; that is
	// treated as vacuously satisfied, the same convention drift.Score already
	// uses for a contract with no required steps, rather than as a zero that
	// would otherwise sink Effectiveness for a fast development check.
	triggerF1 := 1.0
	var triggerUnknown int
	var triggerInferred bool
	if len(execRes.Probes) > 0 {
		probes := make([]score.Probe, 0, len(execRes.Probes))
		for _, p := range execRes.Probes {
			if p.Unknown {
				probes = append(probes, score.Probe{Prompt: p.Probe.Prompt, Positive: p.Probe.Positive, Unknown: true, Reason: p.Reason})
				continue
			}
			fired, inferred := score.DetectFiring(req.Skill, p.Caps, p.Events)
			probes = append(probes, score.Probe{Prompt: p.Probe.Prompt, Positive: p.Probe.Positive, Fired: fired, Inferred: inferred})
		}
		f1, ferr := score.TriggerF1(probes)
		if ferr != nil {
			return nil, ferr
		}
		triggerF1 = f1.F1
		triggerUnknown = f1.Unknown
		triggerInferred = f1.AnyInferred
	}

	promptFor := func(taskID string) string {
		for _, t := range plan.Tasks {
			if t.ID == taskID {
				return t.PromptMD
			}
		}
		return ""
	}

	var modelPanelOut []report.PanelMember
	for _, m := range panel {
		modelPanelOut = append(modelPanelOut, report.PanelMember{Agent: m.Agent, Model: m.Model, Class: string(m.Class)})
	}

	var members []report.MemberInput
	var usedJudge bool
	for _, m := range panel {
		if !scheduled[m] {
			continue
		}
		mi := report.MemberInput{
			Member:  report.PanelMember{Agent: m.Agent, Model: m.Model, Class: string(m.Class)},
			Healthy: healthy[m],
			Detail:  healthDetail[m],
		}
		if !mi.Healthy {
			members = append(members, mi)
			continue
		}

		g := grouped[m]
		if g == nil {
			g = newMemberOutcomes()
		}

		skillTasks, skillPartial, skillPartialReason := taskPasses(g.skill)
		if len(skillTasks) == 0 {
			return nil, fmt.Errorf("whetstone eval: member %s/%s produced no measurable skill run", m.Agent, m.Model)
		}
		reliability, err := score.Reliability(skillTasks, plan.K)
		if err != nil {
			return nil, err
		}

		efficiency := 1.0
		if skillTok, blTok := tokenTotals(g.skill), tokenTotals(g.baseline); len(skillTok) > 0 && len(blTok) > 0 {
			sm, smErr := score.Median(skillTok)
			bm, bmErr := score.Median(blTok)
			if smErr == nil && bmErr == nil {
				if e, eerr := score.Efficiency(sm, bm); eerr == nil {
					efficiency = e
				}
			}
		}

		var upliftVal float64
		metaPartial, metaPartialReason := skillPartial, skillPartialReason
		if len(g.baseline) > 0 {
			baselineTasks, blPartial, blPartialReason := taskPasses(g.baseline)
			baselinePassRate := reliability
			if len(baselineTasks) > 0 {
				if bp, berr := score.Reliability(baselineTasks, plan.K); berr == nil {
					baselinePassRate = bp
				}
			}
			metaPartial = metaPartial || blPartial
			if !skillPartial && blPartial {
				metaPartialReason = blPartialReason
			}

			upliftVal = score.UpliftFromPassRates(reliability, baselinePassRate)
			if judgeTrusted && judge != nil {
				if verdicts, jerr := pairwiseVerdicts(ctx, judge, promptFor, g); jerr == nil && len(verdicts) > 0 {
					if jv, uerr := score.UpliftFromJudge(verdicts); uerr == nil {
						upliftVal = jv
						usedJudge = true
					}
				}
			}
		} else {
			// No baseline was planned for this tier (Smoke): there is nothing
			// to compare against, so Uplift is left neutral rather than
			// penalizing (or crediting) the skill for a comparison that was
			// never run.
			upliftVal = 0.5
		}
		mi.MetaPartial = metaPartial
		mi.MetaPartialReason = metaPartialReason

		mi.Pillars = score.Pillars{TriggerF1: triggerF1, Reliability: reliability, Uplift: upliftVal, Efficiency: efficiency}

		var driftResults []drift.Result
		for _, outs := range g.skill {
			for _, o := range outs {
				semantic := semanticScore(ctx, judge, ct, func() string { return loadTranscript(o.ArtifactDir) })
				obs, oerr := drift.Observe(ct, o.Events)
				if oerr != nil {
					return nil, oerr
				}
				dr, derr := drift.Score(obs, semantic, drift.DefaultWeights)
				if derr != nil {
					return nil, derr
				}
				driftResults = append(driftResults, dr)
			}
		}
		mi.Drift = driftResults

		members = append(members, mi)
	}

	rep, err := report.Compose(report.ComposeInput{
		Skill: req.Skill, SpecVersion: specVersion, Tier: string(req.Tier), SuiteRef: suiteRef,
		EngineVersion: d.EngineVersion, ModelPanel: modelPanelOut, PanelComplete: execRes.PanelComplete,
		Members: members, Void: voidTasks,
		JudgeTrusted: usedJudge, JudgeKappa: judgeKappa, JudgeLabeledBy: judgeLabeledBy,
		TriggerInferred: triggerInferred, TriggerUnknown: triggerUnknown,
		StartedAt: now(), FinishedAt: now(),
	})
	if err != nil {
		return nil, err
	}

	// 10. Persist.
	for _, mr := range rep.Members {
		if err := d.Store.SaveScore(evalID, store.ScoreRow{
			Agent: mr.Member.Agent, Model: mr.Member.Model,
			TriggerF1: mr.Pillars.TriggerF1, Reliability: mr.Pillars.Reliability,
			Uplift: mr.Pillars.Uplift, Efficiency: mr.Pillars.Efficiency,
			Effectiveness: mr.Effectiveness, Adherence: mr.Drift.Mean, Drift: 100 - mr.Drift.Mean,
			Grade: mr.DriftGrade, Healthy: mr.Healthy,
		}); err != nil {
			return nil, err
		}
	}

	var repBuf bytes.Buffer
	if err := rep.Save(&repBuf); err != nil {
		return nil, err
	}
	if err := d.Store.SaveReport(evalID, repBuf.Bytes(), store.ReportMeta{
		Headline: rep.Headline, PanelComplete: rep.PanelComplete, RobustnessGap: rep.RobustnessGap,
	}); err != nil {
		return nil, err
	}
	if err := d.Store.FinishEval(evalID, "done"); err != nil {
		return nil, err
	}

	// 11. Write the sidecar report files and announce the result.
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
			"headline": rep.Headline, "panel_complete": rep.PanelComplete, "uplift_source": string(rep.UpliftSource),
		}); err != nil {
			return nil, err
		}
	} else {
		ui.Success("eval %d for %s: %.1f effectiveness (tier=%s panel_complete=%v uplift=%s)",
			evalID, req.Skill, rep.Headline, req.Tier, rep.PanelComplete, rep.UpliftSource)
	}

	return rep, nil
}

// memberOutcomes groups one panel member's run outcomes by condition and
// task id.
type memberOutcomes struct {
	skill    map[string][]runner.Outcome
	baseline map[string][]runner.Outcome
}

func newMemberOutcomes() *memberOutcomes {
	return &memberOutcomes{skill: map[string][]runner.Outcome{}, baseline: map[string][]runner.Outcome{}}
}

// taskPasses turns one member's per-task outcomes into score.TaskPasses,
// excluding a task whose runs all errored (score.TaskPasses.Void) — an
// errored run is neither a pass nor a fail, and a task with no surviving
// measurement must not be counted as a zero. It also reports whether any run
// carried a partial Meta (a resume that could not recover the full record),
// and the first such reason, so a caller can surface it on the report.
func taskPasses(byTask map[string][]runner.Outcome) (ts []score.TaskPasses, metaPartial bool, reason string) {
	for taskID, outs := range byTask {
		tp := score.TaskPasses{TaskID: taskID}
		for _, o := range outs {
			if o.Status == store.StatusError || o.Status == store.StatusTimeout {
				tp.Errored++
				continue
			}
			tp.N++
			if o.VerifierExit != nil && *o.VerifierExit == 0 {
				tp.C++
			}
			if o.MetaPartial && !metaPartial {
				metaPartial = true
				reason = o.MetaPartialReason
			}
		}
		if tp.Void() {
			continue
		}
		ts = append(ts, tp)
	}
	sort.Slice(ts, func(i, j int) bool { return ts[i].TaskID < ts[j].TaskID })
	return ts, metaPartial, reason
}

// tokenTotals is the per-run total token spend (input+output) across every
// task in byTask, excluding runs that never measured anything.
func tokenTotals(byTask map[string][]runner.Outcome) []float64 {
	var out []float64
	for _, outs := range byTask {
		for _, o := range outs {
			if o.Status == store.StatusError || o.Status == store.StatusTimeout {
				continue
			}
			out = append(out, float64(o.Meta.InputTokens+o.Meta.OutputTokens))
		}
	}
	return out
}

// loadTranscript reads a run's recorded transcript, or "" if it cannot be
// read — a judge call with no transcript degrades to no evidence rather than
// aborting the eval.
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

// semanticScore averages the judge's per-rule Semantic verdict over a
// contract's semantic rules for one run's transcript. A contract with no
// semantic rules — or no judge available to score them — is vacuously
// satisfied, the same convention drift.Score already applies to a contract
// with no required steps: an unmeasured component must not read as a failed
// one.
func semanticScore(ctx context.Context, judge *score.Judge, ct *contract.Contract, transcript func() string) float64 {
	if judge == nil || len(ct.Semantic) == 0 {
		return 1
	}
	t := transcript()
	if t == "" {
		return 1
	}
	var sum float64
	var n int
	for _, r := range ct.Semantic {
		v, _, err := judge.Semantic(ctx, r, score.Sample{Transcript: t})
		if err != nil {
			continue
		}
		sum += v
		n++
	}
	if n == 0 {
		return 1
	}
	return sum / float64(n)
}

// pairwiseVerdicts judges every task for which this member has both a skill
// and a baseline run, comparing their first recorded attempt's transcript. A
// task missing either side, or whose transcript could not be recovered, is
// skipped rather than failing the whole comparison.
func pairwiseVerdicts(ctx context.Context, judge *score.Judge, promptFor func(string) string, g *memberOutcomes) ([]score.Verdict, error) {
	var verdicts []score.Verdict
	for taskID, skillOuts := range g.skill {
		blOuts, ok := g.baseline[taskID]
		if !ok || len(skillOuts) == 0 || len(blOuts) == 0 {
			continue
		}
		skillT := loadTranscript(skillOuts[0].ArtifactDir)
		blT := loadTranscript(blOuts[0].ArtifactDir)
		if skillT == "" || blT == "" {
			continue
		}
		v, err := judge.Pairwise(ctx, score.Pair{
			TaskID: taskID, Prompt: promptFor(taskID),
			Skill:    score.Sample{Label: "skill", Transcript: skillT},
			Baseline: score.Sample{Label: "baseline", Transcript: blT},
		})
		if err != nil {
			return nil, err
		}
		verdicts = append(verdicts, v)
	}
	return verdicts, nil
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
		return fmt.Errorf("whetstone eval: %w", err)
	}
	// See suitecheck.go's identical call: a prior run killed by something
	// stronger than its own context can leave containers and networks behind.
	drv.Sweep(ctx)

	var gw llm.Gateway
	if g, gerr := newGateway(st.Cache()); gerr == nil {
		gw = g
	}

	d := EvalDeps{
		Store: st, Driver: drv, Gateway: gw, Adapters: agent.Get,
		Now: time.Now, Sleep: time.Sleep, EngineVersion: buildVersion,
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
