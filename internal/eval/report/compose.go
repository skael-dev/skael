package report

import (
	"fmt"
	"math"
	"time"
)

// The headline used to carry a bootstrapped 95% CI, removed rather than
// repaired: it bootstrapped the *mean* of member effectiveness while the
// headline is the *minimum*, and at a two-member panel resampling yields means
// of only {min, midpoint, max}, so the interval was identically [min, max] for
// any input.

// MemberInput is one panel member's measured result, before Compose takes the
// panel minimum.
type MemberInput struct {
	Member PanelMember
	// Score is this member's 0–100 mean over its evals' pass rates, from
	// score.MemberScore. Ignored when Healthy is false.
	Score float64
	// Healthy is false when the member's adapter failed its probe. Such a
	// member contributes nothing to scoring rather than a zero.
	Healthy bool
	Detail  string
	// MetaPartial mirrors runner.Outcome.MetaPartial: true when this member's
	// session metadata was rebuilt from the store's own columns rather than
	// recovered in full.
	MetaPartial       bool
	MetaPartialReason string
}

// TaskInput is one eval's raw results, before Compose excludes void evals.
type TaskInput struct {
	TaskID     string
	Conditions []ConditionReport
	Grades     []GradeNote
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
	// Void lists evals excluded from scoring before any run was attempted.
	// Their TaskIDs are removed from Tasks; the list survives to the report so
	// a reader can see what was excluded and why.
	Void []VoidTask

	// Baseline is the same score with no skill installed, measured on the
	// primary member only. BaselineMeasured is false when no baseline session
	// ran, which is what separates a zero delta from an absent one.
	Baseline         float64
	BaselineMeasured bool
	BaselineWipeout  bool

	TriggerF1       float64
	TriggerInferred bool
	TriggerSource   PanelMember
	TriggerUnknown  int

	GraderModel string

	StartedAt  time.Time
	FinishedAt time.Time
}

// Compose assembles a Report from raw per-member and per-eval measurements:
// the panel-wide headline, the without-skill delta, and the void-eval
// exclusion — a void eval is dropped from Tasks but kept on VoidTasks so the
// report shows what was excluded and why.
func Compose(in ComposeInput) (*Report, error) {
	void := map[string]bool{}
	for _, v := range in.Void {
		void[v.TaskID] = true
	}

	members := make([]MemberReport, 0, len(in.Members))
	headline, found := math.Inf(1), false
	var unhealthy []string
	for _, mi := range in.Members {
		if mi.Healthy && (mi.Score < 0 || mi.Score > 100) {
			return nil, fmt.Errorf("report.Compose: member %s/%s scored %v, which is not in [0,100]",
				mi.Member.Agent, mi.Member.Model, mi.Score)
		}
		mr := MemberReport{
			Member:            mi.Member,
			Healthy:           mi.Healthy,
			Detail:            mi.Detail,
			MetaPartial:       mi.MetaPartial,
			MetaPartialReason: mi.MetaPartialReason,
		}
		if mi.Healthy {
			mr.Effectiveness = mi.Score
			found = true
			// Minimum rather than mean: the claim a published score makes is
			// "this works", and it only works if it works on the weakest model
			// someone will run it on. An unhealthy member is excluded rather
			// than scored zero — otherwise an expired token becomes a publish
			// block, which is infrastructure flakiness presented as a quality
			// verdict.
			headline = math.Min(headline, mi.Score)
		} else {
			unhealthy = append(unhealthy, fmt.Sprintf("%s/%s: %s", mi.Member.Agent, mi.Member.Model, mi.Detail))
		}
		members = append(members, mr)
	}
	if !found {
		return nil, fmt.Errorf("report.Compose: no panel member produced a result (%v)", unhealthy)
	}

	var tasks []TaskReport
	for _, ti := range in.Tasks {
		if void[ti.TaskID] {
			continue
		}
		tasks = append(tasks, TaskReport(ti))
	}

	rep := &Report{
		SchemaVersion:   SchemaVersion,
		Skill:           in.Skill,
		SpecVersion:     in.SpecVersion,
		Tier:            in.Tier,
		SuiteRef:        in.SuiteRef,
		EngineVersion:   in.EngineVersion,
		ModelPanel:      in.ModelPanel,
		PanelComplete:   in.PanelComplete,
		Headline:        headline,
		BaselineWipeout: in.BaselineWipeout,
		TriggerF1:       in.TriggerF1,
		Members:         members,
		Tasks:           tasks,
		VoidTasks:       in.Void,
		TriggerInferred: in.TriggerInferred,
		TriggerSource:   in.TriggerSource,
		TriggerUnknown:  in.TriggerUnknown,
		GraderModel:     in.GraderModel,
		StartedAt:       in.StartedAt,
		FinishedAt:      in.FinishedAt,
	}
	if in.BaselineMeasured {
		rep.Baseline = in.Baseline
		rep.Delta = headline - in.Baseline
		rep.DeltaMeasured = true
	}
	return rep, nil
}
