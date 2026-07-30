// Package report defines the machine-readable result of an eval run: the
// schema a CI job reads, a later phase posts, and a future UI charts.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/skael-dev/skael/internal/eval/drift"
	"github.com/skael-dev/skael/internal/eval/score"
	"github.com/skael-dev/skael/internal/eval/suite"
)

// SchemaVersion is the report.json schema's own version, distinct from the
// spec version of the skill it measures. Load refuses a report from a newer
// schema rather than misreading it: a field that changed meaning is worse
// than a field that is missing.
const SchemaVersion = 1

// PanelMember identifies one model-panel entry on the report.
type PanelMember struct {
	Agent      string `json:"agent"`
	Model      string `json:"model"`
	Class      string `json:"class"`
	CLIVersion string `json:"cli_version"`
}

// MemberReport is one panel member's measured result.
type MemberReport struct {
	Member        PanelMember   `json:"member"`
	Pillars       score.Pillars `json:"pillars"`
	Effectiveness float64       `json:"effectiveness"`
	Drift         drift.Agg     `json:"drift"`
	DriftGrade    string        `json:"drift_grade"`
	// Healthy is false when the member's adapter failed its probe. Such a
	// member contributes nothing to the headline rather than a zero.
	Healthy bool   `json:"healthy"`
	Detail  string `json:"detail,omitempty"`
}

// ConditionReport is the pass rate for one task's condition (skill or
// baseline), across models.
type ConditionReport struct {
	Condition string `json:"condition"`
	Model     string `json:"model"`
	Passes    int    `json:"passes"`
	Runs      int    `json:"runs"`
}

// RunDrift is one run's drift measurement.
type RunDrift struct {
	Model      string            `json:"model"`
	Attempt    int               `json:"attempt"`
	Components drift.Components  `json:"components"`
	Adherence  float64           `json:"adherence"`
	Violations []drift.Violation `json:"violations,omitempty"`
}

// JudgeNote is one judge verdict summarised for the report.
type JudgeNote struct {
	Model    string   `json:"model"`
	Winner   string   `json:"winner"`
	Margin   float64  `json:"margin"`
	Evidence []string `json:"evidence,omitempty"`
	Votes    int      `json:"votes"`
}

// TaskReport is one non-void task's results.
type TaskReport struct {
	TaskID     string            `json:"task_id"`
	Kind       string            `json:"kind"`
	Split      string            `json:"split"`
	Conditions []ConditionReport `json:"conditions,omitempty"`
	Drift      []RunDrift        `json:"drift,omitempty"`
	Judge      []JudgeNote       `json:"judge,omitempty"`
}

// VoidTask is a task excluded from scoring by the oracle gate, listed rather
// than merely dropped: a suite quietly losing tasks changes what the score
// means, and the reader has to be able to see it.
type VoidTask struct {
	TaskID string `json:"task_id"`
	Reason string `json:"reason"`
}

// Iteration is one repair loop's outcome, when the eval ran with repair
// enabled.
type Iteration struct {
	N                int      `json:"n"`
	DevEffectiveness float64  `json:"dev_effectiveness"`
	Applied          int      `json:"applied"`
	Pruned           []string `json:"pruned,omitempty"`
	Notes            []string `json:"notes,omitempty"`
}

// Report is the machine-readable result of one eval run against one skill.
type Report struct {
	SchemaVersion int    `json:"schema_version"`
	Skill         string `json:"skill"`
	SpecVersion   int    `json:"spec_version"`
	Tier          string `json:"tier"`
	SuiteRef      string `json:"suite_ref"`
	EngineVersion string `json:"engine_version"`

	ModelPanel    []PanelMember `json:"model_panel"`
	PanelComplete bool          `json:"panel_complete"`

	Headline   float64    `json:"headline"`
	HeadlineCI [2]float64 `json:"headline_ci"`

	UpliftSource score.UpliftSource `json:"uplift_source"`
	// JudgeKappa is nil when no judge was calibrated for this run, as opposed
	// to a judge calibrated at κ=0 — the two are different facts and a bare
	// float64 cannot distinguish them.
	JudgeKappa     *float64 `json:"judge_kappa,omitempty"`
	JudgeLabeledBy string   `json:"judge_labeled_by,omitempty"`

	Members []MemberReport `json:"members"`
	// RobustnessGap is 0 both when it genuinely measured zero and when it
	// could not be computed at all — score.Matrix.ByClass returns ok == false
	// when a class has zero or more than one member, so a strong/floor
	// comparison is not always defined. HasRobustnessGap disambiguates the
	// two: a zero value would otherwise read as "the floor model kept up",
	// the opposite of "we could not tell".
	RobustnessGap    float64 `json:"robustness_gap"`
	HasRobustnessGap bool    `json:"has_robustness_gap"`

	Tasks     []TaskReport `json:"tasks"`
	VoidTasks []VoidTask   `json:"void_tasks,omitempty"`

	TriggerInferred   bool     `json:"trigger_inferred"`
	Unevaluable       int      `json:"unevaluable"`
	UnevaluableDetail []string `json:"unevaluable_detail,omitempty"`

	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`

	Iterations []Iteration `json:"iterations,omitempty"`
}

// Save writes r as indented JSON: a human opens this file when a score
// surprises them.
func (r *Report) Save(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

// Load reads a Report and rejects one written by a newer schema: a field
// that changed meaning is worse than a field that is missing, so a newer
// report read by an older binary must fail loudly rather than be
// misinterpreted.
func Load(r io.Reader) (*Report, error) {
	var probe struct {
		SchemaVersion int `json:"schema_version"`
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("report.Load: %w", err)
	}
	if err := json.Unmarshal(b, &probe); err != nil {
		return nil, fmt.Errorf("report.Load: %w", err)
	}
	if probe.SchemaVersion > SchemaVersion {
		return nil, fmt.Errorf("report.Load: schema version %d is newer than this binary understands (%d)", probe.SchemaVersion, SchemaVersion)
	}
	var rep Report
	if err := json.Unmarshal(b, &rep); err != nil {
		return nil, fmt.Errorf("report.Load: %w", err)
	}
	return &rep, nil
}

// Comparable reports whether two reports measure the same thing, and why not
// when they do not.
//
// This is the difference between a trend and a misleading chart. A score is a
// measurement against a specific suite, evaluated by a specific model panel, at
// a specific tier — change any of those and "the score dropped" is not a fact
// about the skill. What deliberately does *not* affect comparability is the spec
// version and the score itself: comparing v3 against v4 is the only question
// this method exists to answer.
func (r *Report) Comparable(o *Report) (bool, string) {
	switch {
	case r == nil || o == nil:
		return false, "one of the reports is absent"
	case r.SchemaVersion != o.SchemaVersion:
		return false, fmt.Sprintf("different report schemas (%d and %d)", r.SchemaVersion, o.SchemaVersion)
	case r.Skill != o.Skill:
		return false, fmt.Sprintf("different skills (%s and %s)", r.Skill, o.Skill)
	case r.SuiteRef != o.SuiteRef:
		return false, fmt.Sprintf("different suites (%s and %s): the tasks are not the same", suite.ShortRef(r.SuiteRef), suite.ShortRef(o.SuiteRef))
	case r.Tier != o.Tier:
		return false, fmt.Sprintf("different tiers (%s and %s): a smoke score and a full score are not the same measurement", r.Tier, o.Tier)
	case !samePanel(r.ModelPanel, o.ModelPanel):
		return false, "different model panels: a score change could be the models rather than the skill"
	case r.PanelComplete != o.PanelComplete:
		return false, "one panel was incomplete, so its minimum was taken over fewer members"
	case r.UpliftSource != o.UpliftSource:
		return false, fmt.Sprintf("Uplift came from different sources (%s and %s)", r.UpliftSource, o.UpliftSource)
	}
	return true, ""
}

// samePanel compares two panels as an ordered multiset of (agent, model,
// class) triples — panel order is an implementation detail of the planner,
// so a reordered panel is the same panel.
func samePanel(a, b []PanelMember) bool {
	if len(a) != len(b) {
		return false
	}
	as, bs := sortedKeys(a), sortedKeys(b)
	for i := range as {
		if as[i] != bs[i] {
			return false
		}
	}
	return true
}

func sortedKeys(ms []PanelMember) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Agent + "\x00" + m.Model + "\x00" + m.Class
	}
	sort.Strings(out)
	return out
}
