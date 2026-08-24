// Package report defines the machine-readable result of an eval run: the
// schema a CI job reads, the worker posts, and the UI charts.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/skael-dev/skael/internal/eval/score"
	"github.com/skael-dev/skael/internal/eval/store"
	"github.com/skael-dev/skael/internal/eval/suite"
)

// SchemaVersion is the report schema. Load refuses a newer schema.
// Version 2 is the expectation pass rate; version 1 was a geometric mean.
const SchemaVersion = 2

// PanelMember identifies one model-panel entry on the report.
type PanelMember struct {
	Agent      string `json:"agent"`
	Model      string `json:"model"`
	Class      string `json:"class"`
	CLIVersion string `json:"cli_version"`
}

// MemberReport is one panel member's measured result.
type MemberReport struct {
	Member PanelMember `json:"member"`
	// Effectiveness is this member's 0–100 score. The name predates the
	// pipeline change; renaming it would break stored decoders.
	Effectiveness float64 `json:"effectiveness"`
	// Healthy is false when the member's probe failed.
	Healthy bool   `json:"healthy"`
	Detail  string `json:"detail,omitempty"`
	// MetaPartial is true when session metadata was rebuilt from store columns
	// rather than the run's artifact.
	MetaPartial       bool   `json:"meta_partial,omitempty"`
	MetaPartialReason string `json:"meta_partial_reason,omitempty"`
}

// ConditionReport tallies expectations for one eval/condition/model.
type ConditionReport struct {
	Condition store.Condition `json:"condition"`
	Model     string          `json:"model"`
	Passes    int             `json:"passes"`
	Runs      int             `json:"runs"`
}

// GradeNote is one graded session's expectations and evidence.
type GradeNote struct {
	Model        string              `json:"model"`
	Condition    store.Condition     `json:"condition"`
	Attempt      int                 `json:"attempt"`
	Expectations []score.Expectation `json:"expectations,omitempty"`
}

// TaskReport is one non-void eval's results.
type TaskReport struct {
	TaskID     string            `json:"task_id"`
	Conditions []ConditionReport `json:"conditions,omitempty"`
	Grades     []GradeNote       `json:"grades,omitempty"`
}

// VoidTask is an eval excluded from scoring, listed so the reader can see it.
type VoidTask struct {
	TaskID string `json:"task_id"`
	Reason string `json:"reason"`
}

// DroppedGrade is a session that ran but whose grade call failed after every
// retry. It is dropped from the denominator rather than scored as a failure,
// and listed here so the reader can see what the score did not include.
type DroppedGrade struct {
	TaskID    string `json:"task_id"`
	Condition string `json:"condition"`
	Attempt   int    `json:"attempt"`
	Reason    string `json:"reason"`
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

	// Headline is the published 0–100 score: minimum across healthy members.
	Headline float64 `json:"headline"`
	// Baseline is the no-skill measurement. DeltaMeasured is false when no
	// baseline ran — a zero delta and an absent delta are different facts.
	Baseline      float64 `json:"baseline"`
	Delta         float64 `json:"delta"`
	DeltaMeasured bool    `json:"delta_measured"`
	// BaselineWipeout is true when the baseline passed no expectation at all.
	BaselineWipeout bool `json:"baseline_wipeout,omitempty"`

	// TokensMedian is the median token spend with the skill installed;
	// TokensMedianBaseline is the same without it.
	TokensMedian         int64 `json:"tokens_median,omitempty"`
	TokensMedianBaseline int64 `json:"tokens_median_baseline,omitempty"`

	// TriggerF1 is the trigger smoke check. Reported beside the headline, not
	// folded into it.
	TriggerF1 float64 `json:"trigger_f1"`

	Members []MemberReport `json:"members"`

	Tasks     []TaskReport `json:"tasks"`
	VoidTasks []VoidTask   `json:"void_tasks,omitempty"`

	DroppedGrades []DroppedGrade `json:"dropped_grades,omitempty"`

	TriggerInferred bool `json:"trigger_inferred"`
	// TriggerSource is the panel member the trigger probes ran on.
	TriggerSource PanelMember `json:"trigger_source,omitempty"`
	// TriggerUnknown is the count of probes excluded from the confusion matrix
	// because their session could not be measured.
	TriggerUnknown int `json:"trigger_unknown,omitempty"`

	// GraderModel identifies which model graded this run.
	GraderModel string `json:"grader_model,omitempty"`

	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
}

// Save writes r as indented JSON.
func (r *Report) Save(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

// Load reads a Report. Rejects a newer schema version.
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

// Comparable reports whether two reports measure the same thing. A changed
// suite, panel, tier, grader, or engine version makes scores incomparable.
func (r *Report) Comparable(o *Report) (bool, string) {
	switch {
	case r == nil || o == nil:
		return false, "one of the reports is absent"
	case r.SchemaVersion != o.SchemaVersion:
		return false, fmt.Sprintf("different report schemas (%d and %d)", r.SchemaVersion, o.SchemaVersion)
	case r.Skill != o.Skill:
		return false, fmt.Sprintf("different skills (%s and %s)", r.Skill, o.Skill)
	case r.SuiteRef != o.SuiteRef:
		return false, fmt.Sprintf("different eval sets (%s and %s): the evals are not the same", suite.ShortRef(r.SuiteRef), suite.ShortRef(o.SuiteRef))
	case r.EngineVersion != o.EngineVersion:
		return false, fmt.Sprintf("different engine versions (%s and %s): a scoring-logic change can move the number without the skill changing", r.EngineVersion, o.EngineVersion)
	case r.Tier != o.Tier:
		return false, fmt.Sprintf("different tiers (%s and %s): a smoke score and a full score are not the same measurement", r.Tier, o.Tier)
	case !samePanel(r.ModelPanel, o.ModelPanel):
		return false, "different model panels: a score change could be the models rather than the skill"
	case !sameCLIVersions(r.ModelPanel, o.ModelPanel):
		return false, "different agent CLI versions on the panel: a score change could be the CLI rather than the skill"
	case r.PanelComplete != o.PanelComplete:
		return false, "one panel was incomplete, so its minimum was taken over fewer members"
	case r.GraderModel != o.GraderModel:
		return false, fmt.Sprintf("different grader models (%q and %q): a different grader can move the number without the skill changing", r.GraderModel, o.GraderModel)
	}
	return true, ""
}

// samePanel compares two panels as a sorted multiset of (agent, model, class).
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

// sameCLIVersions compares CLIVersion as a sorted multiset. Called after
// samePanel confirms the members match.
func sameCLIVersions(a, b []PanelMember) bool {
	if len(a) != len(b) {
		return false
	}
	as, bs := sortedVersions(a), sortedVersions(b)
	for i := range as {
		if as[i] != bs[i] {
			return false
		}
	}
	return true
}

func sortedVersions(ms []PanelMember) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.CLIVersion
	}
	sort.Strings(out)
	return out
}
