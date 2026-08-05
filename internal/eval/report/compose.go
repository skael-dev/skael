package report

import (
	"fmt"
	"time"

	"github.com/skael-dev/skael/internal/eval/drift"
	"github.com/skael-dev/skael/internal/eval/score"
	"github.com/skael-dev/skael/internal/eval/spec"
)

// bootstrapIters and bootstrapSeed fix Compose's confidence interval so the
// same inputs always produce the same report — see score.Bootstrap's own
// doc for why the seed is explicit rather than a shared global generator.
const (
	bootstrapIters = 2000
	bootstrapSeed  = 1
)

// MemberInput is one panel member's raw measurements, from which Compose
// derives Effectiveness and drift aggregation.
type MemberInput struct {
	Member  PanelMember
	Pillars score.Pillars
	// Healthy is false when the member's adapter failed its probe. Such a
	// member contributes nothing to scoring rather than a zero.
	Healthy bool
	Detail  string
	// Drift is this member's per-run drift results, already scored by
	// drift.Score. Empty when the member is unhealthy.
	Drift []drift.Result
	// MetaPartial mirrors runner.Outcome.MetaPartial: true when this member's
	// session metadata was rebuilt from the store's own columns rather than
	// recovered in full. Compose passes it straight through onto MemberReport
	// so a reader can tell a figure rests on partial metadata.
	MetaPartial       bool
	MetaPartialReason string
}

// TaskInput is one task's raw results, before Compose excludes void tasks.
type TaskInput struct {
	TaskID     string
	Kind       string
	Split      string
	Conditions []ConditionReport
	Drift      []RunDrift
	Judge      []JudgeNote
}

// ComposeInput is everything Compose needs to assemble a Report.
type ComposeInput struct {
	Skill         string
	SpecVersion   int
	Tier          string
	SuiteRef      string
	EngineVersion string

	ModelPanel    []PanelMember
	PanelComplete bool

	Members []MemberInput
	Tasks   []TaskInput
	// Void lists tasks the oracle gate excluded from scoring, before any run
	// was ever attempted. Their TaskIDs are removed from Tasks before
	// scoring; the list itself survives to the report so a reader can see
	// what was excluded and why.
	//
	// This is a different exclusion from score.TaskPasses.Void, which is a
	// task whose runs all errored — see that doc for why "void" means two
	// different things here and how their report-level visibility currently
	// differs.
	Void []VoidTask

	// JudgeTrusted is whether the judge's calibration cleared the κ floor
	// (a later task selects the floor itself). false demotes UpliftSource to
	// the pass-rate fallback, but JudgeKappa is still recorded — it is the κ
	// that caused the demotion.
	//
	// JudgeKappa is nil when no judge was calibrated for this run at all — a
	// run with no gateway available produces no calibration, and that must
	// not read as a judge that scored κ = 0.0. Compose passes it straight
	// through to Report.JudgeKappa without manufacturing a value.
	JudgeTrusted   bool
	JudgeKappa     *float64
	JudgeLabeledBy string
	// JudgeModel is passed straight through onto Report.JudgeModel — see that
	// field's doc for why only a model name, not a gateway base URL, is
	// available to record here.
	JudgeModel string

	TriggerInferred bool
	// TriggerSource is passed through to Report.TriggerSource unchanged: the
	// single panel member the trigger probes ran on.
	TriggerSource PanelMember
	// TriggerUnknown is score.F1Result.Unknown passed through: the count of
	// trigger probes whose session could not be measured (e.g. it errored) and
	// so were excluded from every quadrant of the trigger confusion matrix
	// rather than counted as a miss.
	TriggerUnknown int
	Unevaluable    int
	// UnevaluableDetail is passed through to the report as given: Compose
	// does not aggregate it per-run or de-duplicate it. The caller owns
	// assembling this list from whatever per-run observations it holds.
	UnevaluableDetail []string

	StartedAt  time.Time
	FinishedAt time.Time

	Iterations []Iteration
}

// Compose assembles a Report from raw per-member and per-task measurements:
// each member's Effectiveness and drift aggregate, the panel-wide headline
// and its bootstrap confidence interval, the strong/floor robustness gap
// when it is defined, the judge demotion when the judge is untrusted, and
// the void-task exclusion — a void task is dropped from Tasks but kept on
// VoidTasks so the report shows what was excluded and why.
func Compose(in ComposeInput) (*Report, error) {
	void := map[string]bool{}
	for _, v := range in.Void {
		void[v.TaskID] = true
	}

	var (
		matrix  score.Matrix
		members []MemberReport
	)
	for _, mi := range in.Members {
		// Validated regardless of health: Effectiveness's own validation is
		// skipped for an unhealthy member (there is nothing to compute), but an
		// out-of-range or judge-derived pillar value must not reach the report
		// unchecked just because the member that carried it never got that far.
		if err := mi.Pillars.Validate(); err != nil {
			return nil, fmt.Errorf("report.Compose: member %s/%s: %w", mi.Member.Agent, mi.Member.Model, err)
		}

		entry := score.PanelEntry{
			Member:  score.Member{Agent: mi.Member.Agent, Model: mi.Member.Model, Class: spec.ModelTier(mi.Member.Class)},
			Pillars: mi.Pillars,
			Healthy: mi.Healthy,
			Detail:  mi.Detail,
		}

		mr := MemberReport{
			Member:            mi.Member,
			Pillars:           mi.Pillars,
			Healthy:           mi.Healthy,
			Detail:            mi.Detail,
			MetaPartial:       mi.MetaPartial,
			MetaPartialReason: mi.MetaPartialReason,
		}

		if mi.Healthy {
			eff, err := score.Effectiveness(mi.Pillars, score.DefaultExponents)
			if err != nil {
				return nil, fmt.Errorf("report.Compose: member %s/%s: %w", mi.Member.Agent, mi.Member.Model, err)
			}
			entry.Effectiveness = eff
			mr.Effectiveness = eff

			if len(mi.Drift) > 0 {
				agg, err := drift.Aggregate(mi.Drift)
				if err != nil {
					return nil, fmt.Errorf("report.Compose: member %s/%s: %w", mi.Member.Agent, mi.Member.Model, err)
				}
				entry.Drift = agg
				entry.Grade = drift.Grade(agg.Mean, agg.Worst)
				mr.Drift = agg
				mr.DriftGrade = entry.Grade
			}
		}

		matrix.Entries = append(matrix.Entries, entry)
		members = append(members, mr)
	}

	headline, err := matrix.Headline()
	if err != nil {
		return nil, fmt.Errorf("report.Compose: %w", err)
	}

	var samples []float64
	for _, e := range matrix.Entries {
		if e.Healthy {
			samples = append(samples, e.Effectiveness)
		}
	}
	lo, hi, err := score.Bootstrap(samples, bootstrapIters, bootstrapSeed)
	if err != nil {
		return nil, fmt.Errorf("report.Compose: %w", err)
	}

	var gap *float64
	strong, okStrong := matrix.ByClass(spec.TierStrong)
	floor, okFloor := matrix.ByClass(spec.TierFloor)
	if okStrong && okFloor {
		// ByClass already refused an unhealthy match; RobustnessGap additionally
		// refuses an Agg with N==0 (a healthy member that simply has no drift
		// runs), so a gap is only ever computed from two real measurements.
		if g, err := drift.RobustnessGap(strong.Drift, floor.Drift); err == nil {
			gap = &g
		}
	}

	upliftSource := score.UpliftJudge
	if !in.JudgeTrusted {
		upliftSource = score.UpliftPassRate
	}

	var tasks []TaskReport
	for _, ti := range in.Tasks {
		if void[ti.TaskID] {
			continue
		}
		tasks = append(tasks, TaskReport(ti))
	}

	return &Report{
		SchemaVersion:     SchemaVersion,
		Skill:             in.Skill,
		SpecVersion:       in.SpecVersion,
		Tier:              in.Tier,
		SuiteRef:          in.SuiteRef,
		EngineVersion:     in.EngineVersion,
		ModelPanel:        in.ModelPanel,
		PanelComplete:     in.PanelComplete,
		Headline:          headline,
		HeadlineCI:        [2]float64{lo, hi},
		UpliftSource:      upliftSource,
		JudgeKappa:        in.JudgeKappa,
		JudgeLabeledBy:    in.JudgeLabeledBy,
		JudgeModel:        in.JudgeModel,
		Members:           members,
		RobustnessGap:     gap,
		Tasks:             tasks,
		VoidTasks:         in.Void,
		TriggerInferred:   in.TriggerInferred,
		TriggerSource:     in.TriggerSource,
		TriggerUnknown:    in.TriggerUnknown,
		Unevaluable:       in.Unevaluable,
		UnevaluableDetail: in.UnevaluableDetail,
		StartedAt:         in.StartedAt,
		FinishedAt:        in.FinishedAt,
		Iterations:        in.Iterations,
	}, nil
}
