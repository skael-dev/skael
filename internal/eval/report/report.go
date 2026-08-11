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

// SchemaVersion is the report.json schema's own version, distinct from the
// spec version of the skill it measures. Load refuses a report from a newer
// schema rather than misreading it: a field that changed meaning is worse
// than a field that is missing.
//
// Version 2 is the expectation pass rate. Version 1 was a weighted geometric
// mean of four pillars, on the same 0–100 scale and measuring something else.
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
	// Effectiveness is this member's 0–100 score: the mean of its evals' pass
	// rates. The field keeps the name it had when it was a geometric mean of
	// four pillars, because it still answers the same question and renaming it
	// would break every stored decoder for no gain.
	Effectiveness float64 `json:"effectiveness"`
	// Healthy is false when the member's adapter failed its probe. Such a
	// member contributes nothing to the headline rather than a zero.
	Healthy bool   `json:"healthy"`
	Detail  string `json:"detail,omitempty"`
	// MetaPartial is true when this member's session metadata was rebuilt from
	// the store's own columns rather than recovered in full (a resumed run
	// whose recorded artifact could not be re-read).
	MetaPartial       bool   `json:"meta_partial,omitempty"`
	MetaPartialReason string `json:"meta_partial_reason,omitempty"`
}

// ConditionReport is one eval's expectation tally for one condition (skill or
// baseline), for one model. Passes and Runs are expectations passed and
// expectations graded, summed over that condition's runs.
type ConditionReport struct {
	Condition store.Condition `json:"condition"`
	Model     string          `json:"model"`
	Passes    int             `json:"passes"`
	Runs      int             `json:"runs"`
}

// GradeNote is one graded session, kept so a surprising score can be read back
// to the expectation and the evidence that produced it.
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

// VoidTask is an eval excluded from scoring, listed rather than merely
// dropped: a suite quietly losing evals changes what the score means, and the
// reader has to be able to see it.
type VoidTask struct {
	TaskID string `json:"task_id"`
	Reason string `json:"reason"`
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

	// Headline is the single published 0–100 score: the lowest member score
	// across healthy panel members.
	Headline float64 `json:"headline"`
	// Baseline is the same measurement with no skill installed, and Delta is
	// Headline minus it. Together they answer "does this skill help", which no
	// pass rate answers on its own.
	//
	// DeltaMeasured is false when no baseline session ran. A zero delta and an
	// absent delta are different facts: the first says the skill changed
	// nothing, the second says nobody looked.
	Baseline      float64 `json:"baseline"`
	Delta         float64 `json:"delta"`
	DeltaMeasured bool    `json:"delta_measured"`
	// BaselineWipeout is true when the baseline passed no expectation at all,
	// which is also what a broken baseline harness looks like.
	BaselineWipeout bool `json:"baseline_wipeout,omitempty"`

	// TokensMedian is the median total token spend of a scored session with the
	// skill, and TokensMedianBaseline the same without it. Reported beside the
	// score rather than inside it: a verbose skill that works still works, and
	// nobody notices one tripling its own token bill unless the figure is here.
	TokensMedian         int64 `json:"tokens_median,omitempty"`
	TokensMedianBaseline int64 `json:"tokens_median_baseline,omitempty"`

	// TriggerF1 is the trigger smoke check: does the skill fire when it should
	// and stay silent when it should not. It is reported beside the headline
	// rather than folded into it, and it gates a release rather than scoring
	// one.
	TriggerF1 float64 `json:"trigger_f1"`

	Members []MemberReport `json:"members"`

	Tasks     []TaskReport `json:"tasks"`
	VoidTasks []VoidTask   `json:"void_tasks,omitempty"`

	TriggerInferred bool `json:"trigger_inferred"`
	// TriggerSource is the single panel member the trigger probes ran on.
	// Trigger firing is measured once per eval, on this member.
	TriggerSource PanelMember `json:"trigger_source,omitempty"`
	// TriggerUnknown is the count of trigger probes excluded from the trigger
	// confusion matrix because their session could not be measured — excluded
	// rather than counted as a miss, so an infrastructure failure does not
	// masquerade as a recall failure.
	TriggerUnknown int `json:"trigger_unknown,omitempty"`

	// GraderModel identifies which model graded this run. An operator-
	// configurable grader means the same panel, on the same suite, can produce
	// a different Headline purely because a different model did the grading.
	GraderModel string `json:"grader_model,omitempty"`

	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
}

// Save writes r as indented JSON: a human opens this file when a score
// surprises them.
func (r *Report) Save(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

// Load reads a Report and rejects one written by a newer schema: a field
// that changed meaning is worse than a field that is missing.
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
// measurement against a specific eval set, run by a specific model panel, at a
// specific tier, graded by a specific model — change any of those and "the
// score dropped" is not a fact about the skill. What deliberately does *not*
// affect comparability is the spec version and the score itself: comparing v3
// against v4 is the only question this method exists to answer.
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

// sameCLIVersions compares the CLIVersion recorded against each panel member,
// as a sorted multiset alongside samePanel's identity check. Called only after
// samePanel has already confirmed the two panels have the same members, so a
// mismatch here means the same members were measured with different agent CLI
// builds.
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
