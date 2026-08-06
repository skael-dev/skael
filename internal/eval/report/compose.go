package report

import (
	"errors"
	"fmt"
	"time"

	"github.com/skael-dev/skael/internal/eval/drift"
	"github.com/skael-dev/skael/internal/eval/score"
	"github.com/skael-dev/skael/internal/eval/spec"
)

// The headline used to carry a bootstrapped 95% confidence interval. It was
// removed rather than repaired, for two independent reasons.
//
// It described the wrong statistic: the interval was bootstrapped over the
// *mean* of member effectiveness while the headline is the *minimum*, so the
// published number did not sit inside — or even at the centre of — its own
// stated interval.
//
// And at the panel sizes this product actually runs it could not carry
// information at all. With two members, resampling with replacement yields
// means of only {min, midpoint, max} at probabilities {0.25, 0.5, 0.25}, so
// the 2.5th and 97.5th percentiles land in the min and max blocks every time:
// the "95% CI" was identically [min, max] of the two member scores, for any
// input whatsoever. A real report published [0.0, 69.6] against a headline of
// 0.0 that way.

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
	// UnevaluableDetail is the list of reasons checks could not be performed.
	// Compose de-duplicates it, collapsing repeats into "… (×N)" and capping
	// the result — see dedupeDetail.
	//
	// It used to be passed through verbatim on the stated grounds that the
	// caller owned assembling it. No caller ever did, and one real report
	// rendered 326 list items drawn from about a dozen distinct messages,
	// which is not a list anyone reads. De-duplicating here rather than at the
	// call site means every producer gets it.
	UnevaluableDetail []string
	// BaselineWipeout is passed through to Report.BaselineWipeout — see there.
	BaselineWipeout bool

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
		// Set when any member's adherence could not be measured, so the report
		// can say so once at the top rather than leaving a reader to notice a
		// missing column.
		driftUnmeasurable bool
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
				switch {
				case errors.Is(err, drift.ErrUnmeasurable):
					// Not a defect: too many contract checks could not be
					// performed for this member's adherence to mean anything.
					// Leave Drift absent — a zero would read as "followed the
					// contract not at all", which is a claim nothing here
					// measured — and let the report say so instead.
					mr.DriftUnmeasurable = true
					driftUnmeasurable = true
				case err != nil:
					return nil, fmt.Errorf("report.Compose: member %s/%s: %w", mi.Member.Agent, mi.Member.Model, err)
				default:
					entry.Drift = agg
					mr.Drift = agg
				}
			}
		}

		matrix.Entries = append(matrix.Entries, entry)
		members = append(members, mr)
	}

	headline, err := matrix.Headline()
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
		DriftUnmeasurable: driftUnmeasurable,
		BaselineWipeout:   in.BaselineWipeout,
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
		UnevaluableDetail: dedupeDetail(in.UnevaluableDetail, maxUnevaluableDetail),
		StartedAt:         in.StartedAt,
		FinishedAt:        in.FinishedAt,
		Iterations:        in.Iterations,
	}, nil
}
